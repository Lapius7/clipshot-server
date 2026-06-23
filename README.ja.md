# clipshot-server

[English README is here](README.md)

Go言語で書かれたセルフホスト可能な画像アップロードAPIサーバーです。[clipshot-app](https://github.com/Lapius7/clipshot-app)（Windowsトレイ常駐クライアント）からアップロードされた画像を受け取り、公開URLを返します。ShareXやGyazoの「カスタムアップローダー」バックエンドと同じ発想ですが、全部自分で管理できるのが違いです。

- 実行時の外部依存なし: 画像はローカルディスク、メタデータは単一のSQLiteファイルに保存。
- 単一のDockerイメージとして配布。`docker compose up -d`一発で動くインスタンスが手に入ります。
- トークンベースの認証は「自分のデバイスにキーを配る単一管理者」を想定した設計で、マルチテナントSaaSではありません。

## 目次

- [これが存在する理由](#これが存在する理由)
- [アーキテクチャ](#アーキテクチャ)
- [クイックスタート（Docker Compose）](#クイックスタートdocker-compose)
- [クイックスタート（docker run）](#クイックスタートdocker-run)
- [ソースから実行する](#ソースから実行する)
- [設定リファレンス](#設定リファレンス)
- [トークン管理](#トークン管理)
- [APIリファレンス](#apiリファレンス)
- [HTTPS化する](#https化する)
- [データ配置とバックアップ](#データ配置とバックアップ)
- [セキュリティモデル](#セキュリティモデル)
- [プロジェクト構成](#プロジェクト構成)
- [ロードマップ・既知の制約](#ロードマップ既知の制約)
- [コントリビュート](#コントリビュート)
- [ライセンス](#ライセンス)

## これが存在する理由

ホスト型のスクリーンショット・画像アップロードサービスは便利ですが、画像が実際にどこに保存され、どのくらいの期間残り、誰が見られるのかを気にし始めると不安が出てきます。clipshot-serverは、小さなWindowsホットキーツールを「自分が管理するサーバー」（自宅サーバーでも自前のVPSでも何でも）に向けて、クリップボードの内容を第三者に渡すことなく「キーを押すとURLが手に入る」というワークフローをそのまま実現するために作られています。

意図的に汎用のマルチテナント画像ホスティングを目指していません。ユーザー登録フローも、Webダッシュボードも、課金機能もありません。SaaSプロダクトというより、セルフホストされたShareXカスタムアップローダーのエンドポイントに近いものです。

## アーキテクチャ

```
┌─────────────────┐    HTTPS, Bearerトークン        ┌───────────────────────┐
│  clipshot-app    │ ───────────────────────────▶  │   clipshot-server     │
│ (Windowsトレイ)  │  POST /api/upload (multipart)  │                       │
└─────────────────┘ ◀─────────────────────────────  │  net/http + SQLite    │
        ▲              { "url": "https://..." }     │  + ローカルファイル   │
        │                                             └───────────────────────┘
        │ URLをクリップボードへ書き込み                          │
        ▼                                                          ▼
   ユーザーがURLを貼り付け                                /data/<id>.<ext>
                                                          /data/clipshot.db
```

アップロード時のリクエストフロー:

1. クライアントが `Authorization: Bearer <token>` ヘッダーと `multipart/form-data`（フィールド名 `file`）のボディで `POST /api/upload` を送信。
2. 認証ミドルウェアが提示されたトークンをSHA-256でハッシュ化し、`tokens` テーブルを検索。失効済み・未知のトークンは `401` を返す。
3. トークン単位のレートリミッタ（トークンバケット、[`go-rataliy_lib`](https://github.com/Lapius7/go-rataliy_lib)経由）が設定値を超えるバーストを `429` で拒否し、`Retry-After` ヘッダーを設定。
4. ハンドラは `http.MaxBytesReader` で `MAX_UPLOAD_MB` を強制し、**クライアントが送ってきた`Content-Type`ヘッダーではなく実際のバイト列から本当のコンテンツタイプをスニッフィング**。png/jpeg/gif/webpのホワイトリスト外は `415` で拒否。
5. 推測不可能な高エントロピーのランダムID（base62で16文字、約95ビット。連番でも推測可能でもない）を生成し、`DATA_DIR/<id>.<ext>` にファイルを書き込む。
6. 監査のため `uploads` テーブルに1行記録（ファイル名・コンテンツタイプ・サイズ・所有トークン・タイムスタンプ）。
7. `201 Created` と `{"url": "<BASE_URL>/i/<id>.<ext>"}` を返す。

配信（`GET /i/{id}.{ext}`）は設計上、認証不要です。これは他の画像ホスティングサービスと同様、公開して共有できるリンクであることを意図しているためです。アップロードされた画像のバイト列は書き込み後不変なので、このエンドポイントは長期間有効な `Cache-Control` ヘッダーを返します。

## クイックスタート（Docker Compose）

新規VPSにセットアップする場合はこの方法を推奨します。

```bash
git clone https://github.com/Lapius7/clipshot-server.git
cd clipshot-server
cp .env.example .env
# .env を編集: BASE_URL をこのサーバーが実際に到達可能なhttps://のURLに設定
# 例: https://img.example.com
docker compose up -d
docker compose logs -f
```

初回起動時、アクティブなトークンがデータベースに1つも無い場合、サーバーは自動的に1つ発行し、**一度だけ**ログに出力します。

```
===========================================================
No active tokens found. Created a bootstrap token:
cs_f5df7d560471d91eb73dcaa95b56c189177532c6e50e25d36bf79ef781108a20
Save this now -- it will not be shown again. Use it as the
API key in the clipshot-app client (Authorization: Bearer ...)
===========================================================
```

このトークンをclipshot-appの設定にコピーしてください。失くした場合は[新しいトークンを発行](#トークン管理)してください — 平文は保存されないため、元のトークンを復元する方法はありません。

## クイックスタート（docker run）

Composeを使わない場合:

```bash
docker run -d \
  --name clipshot-server \
  --restart unless-stopped \
  -p 8080:8080 \
  -v $(pwd)/data:/data \
  -e BASE_URL=https://img.example.com \
  ghcr.io/lapius7/clipshot-server:latest
docker logs -f clipshot-server   # 初回起動時にブートストラップトークンを確認
```

> **注:** `ghcr.io` のイメージはまだ公開されていません（[ロードマップ](#ロードマップ既知の制約)参照）。それまでは `docker build -t clipshot-server .` でローカルビルドし、上記のタグをそれに置き換えてください。

## ソースから実行する

Go 1.23以上が必要です。CGOもシステムのSQLiteも不要 — `modernc.org/sqlite` は純Go実装のドライバなので、Goが動く環境ならどこでもビルド・実行できます。

```bash
go build -o clipshot-server ./cmd/server
BASE_URL=https://img.example.com DATA_DIR=./data ./clipshot-server
```

開発中は単に `go run ./cmd/server` でも構いません。

## 設定リファレンス

すべての設定は環境変数経由です（`.env.example` 参照）。

| 変数 | 必須 | デフォルト | 説明 |
|---|---|---|---|
| `BASE_URL` | ✅ | — | 返却される画像URLの構築に使う公開ベースURL。例: `https://img.example.com`。クライアントが実際にサーバーへ到達する方法と一致させること。 |
| `PORT` | | `8080` | HTTPサーバーがリッスンするTCPポート。 |
| `DATA_DIR` | | `/data` | SQLiteデータベースとアップロード画像ファイルの両方を保存するディレクトリ。ここをボリュームとしてマウントする。 |
| `DB_PATH` | | `<DATA_DIR>/clipshot.db` | DBファイルを`DATA_DIR`以外の場所に置きたい場合に上書き。 |
| `MAX_UPLOAD_MB` | | `25` | 許可する最大アップロードサイズ（MB）。ボディを完全に読み込む前に強制される。 |
| `RATE_LIMIT_RPM` | | `30` | トークンごとに許可される1分間のリクエスト数（定常状態のレート）。 |
| `RATE_LIMIT_BURST` | | `10` | 定常レートに加えて許容されるバースト量。 |

## トークン管理

トークンはclipshot-app（またはAPIを話す他の何か）が認証する手段です。自己サインアップは意図的に存在せず、管理者（あなた）がサーバー自体でCLIを使ってトークンを発行します。

```bash
# 実行中のコンテナ内で（Composeのサービス名は"clipshot-server"）
docker compose exec clipshot-server clipshot-server token create -label "desktop-pc"
docker compose exec clipshot-server clipshot-server token create -label "work-laptop"

# IDを指定してトークンを失効させる（IDは作成時にログ出力され、
# uploads/tokensテーブルを直接クエリしても確認できる）
docker compose exec clipshot-server clipshot-server token revoke -id <token-id>
```

設計上の注意点:

- トークンは `crypto/rand` で生成（32バイトのランダム値、16進エンコード、`cs_` プレフィックス付き）。
- 永続化されるのはトークンのSHA-256ハッシュのみ。平文は作成時に一度だけ表示され、二度と取得できません — 失くしたら失効させて新しく発行してください。
- 失効はソフトデリート（`revoked_at` を設定）なので、失効済みトークンIDに紐づくアップロード履歴は監査のために保持されます。
- トークンの有効期限はまだ実装されていません。各トークンは長期間有効なものとして扱い、デバイスを廃棄・侵害された場合は手動で失効させてください。

## APIリファレンス

### `POST /api/upload`

| | |
|---|---|
| 認証 | `Authorization: Bearer <token>`（必須） |
| ボディ | `multipart/form-data`、ファイルフィールド名 **`file`** |
| 対応形式 | image/png, image/jpeg, image/gif, image/webp（ファイル名や宣言されたMIMEタイプではなく、実際のバイト列から検出） |
| 成功 | `201 Created`, `{"url": "https://.../i/<id>.<ext>"}` |
| エラー | `401` トークン無し/無効/失効済み・`400` fileフィールド無し・`413` `MAX_UPLOAD_MB` 超過・`415` 非対応の画像形式・`429` レート制限超過・`500` ストレージ/DB障害 |

例:

```bash
curl -X POST https://img.example.com/api/upload \
  -H "Authorization: Bearer cs_xxx..." \
  -F "file=@screenshot.png"
# => {"url":"https://img.example.com/i/EfhLdkk5I36am0pS.png"}
```

### `GET /i/{id}.{ext}`

保存された画像のバイト列を直接配信します。認証なし — これが公開・共有可能なリンクです。id/拡張子の組がディスク上に存在しない場合は `404` を返します。

### `GET /healthz`

`200 ok` を返します。コンテナのヘルスチェックや死活監視に利用できます。

## HTTPS化する

clipshot-server自体は平のHTTPしか話しません — 手前にTLSを終端するリバースプロキシを置くことを前提としています。これによりGoバイナリ自体はシンプルなままで、既に運用しているプロキシをそのまま再利用できます。clipshot-appは `https://` 以外のURLとは通信を拒否するため、実運用ではこのステップは必須です。

[Caddy](https://caddyserver.com/) を使った例（Let's Encryptによる自動HTTPS）:

```caddyfile
img.example.com {
    reverse_proxy localhost:8080
}
```

どのリバースプロキシでも同じ方法で動作します（Nginx、Traefikなど）— 設定した `PORT` に転送するだけです。

## データ配置とバックアップ

すべて `DATA_DIR` の下にあります（コンテナ内では `/data`、デフォルトのCompose構成ではホスト側の `./data`）。

```
data/
├── clipshot.db        # SQLite: トークン + アップロードメタデータ
├── EfhLdkk5I36am0pS.png
├── 3kQ9z...            .jpg
└── ...
```

バックアップはこの1つのディレクトリをバックアップするだけです — コンテナを停止し、`data/` をコピーして再起動。連携が必要な外部データベースやオブジェクトストアはありません。

## セキュリティモデル

- **通信**: HTTPSはリバースプロキシ経由で運用者の責任範囲です（上記参照）。サーバー自体はTLSを提供しません。
- **認証**: 不透明なBearerトークン。保存時にSHA-256でハッシュ化し、標準ライブラリの定数時間比較ヘルパーで比較します。
- **権限スコープ**: フラットです — 有効なトークンであれば誰でもアップロード可能。失効以外のトークン単位のクォータやスコープ制限はありません。これは「自分のデバイスにトークンを配る単一管理者」のユースケースに合わせた設計で、マルチテナントの信頼境界ではありません。
- **不正利用対策**: トークン単位のレート制限（トークンバケット）と、クライアントの主張に依存しないサーバー側での厳格なアップロードサイズ上限。
- **コンテンツ検証**: クライアントが宣言した `Content-Type` やファイル名拡張子を信用せず、実際のファイルバイト列をスニッフィング（`http.DetectContentType`）し、検出された種類からサーバー側で選んだ拡張子でのみファイルを書き込みます。
- **ID生成**: アップロードIDとトークンIDは `math/rand` ではなく `crypto/rand` を使用 — IDは列挙も推測もできません。
- **このサーバーが守らないもの**: 漏洩したBearerトークンは、失効されるまで完全なアップロード権限を与えます（現時点でリクエスト単位のスコープや有効期限はありません — [ロードマップ](#ロードマップ既知の制約)参照）。トークンはパスワードと同じように扱ってください。

## プロジェクト構成

```
cmd/server/main.go        エントリポイント: 設定ロード、ブートストラップトークン、HTTPサーバー起動
internal/config/          環境変数のロードとバリデーション
internal/db/              SQLite接続 + スキーママイグレーション（tokens, uploadsテーブル）
internal/storage/         Storageインターフェース + ローカルファイルシステム実装
internal/auth/            トークンの作成・検証・失効（保存時はハッシュ化）
internal/idgen/           暗号論的に安全な短いランダムID生成
internal/handler/         HTTPルート: upload, serve, healthz
internal/cli/             `clipshot-server token create|revoke` 管理者向けサブコマンド
```

`storage.Storage` インターフェースは意図的に切り出されており、将来S3互換バックエンドを追加してもHTTPハンドラ側に手を入れる必要がないように設計されています — ロードマップ参照。

## ロードマップ・既知の制約

これは初期段階の骨格であり、完成したプロダクトではありません。既知のギャップを優先度順に挙げます。

- [ ] CI経由で `ghcr.io` にビルド済みイメージを公開（現在は自分でビルドする必要あり）
- [ ] 既存の `storage.Storage` インターフェースを使ったS3互換ストレージバックエンド
- [ ] トークンごとの有効期限・アップロードクォータ
- [ ] アップロード削除エンドポイント（現在アップロードされた画像は永続的で、削除はファイル＋DB行の手動削除のみ）
- [ ] 構造化ログ・メトリクスエンドポイント
- [ ] 自動テスト（現在の検証はビルド・vet・手動のエンドツーエンドアップロード/配信確認のみ）

## コントリビュート

IssueやPull Requestを歓迎します。このプロジェクトは意図的にスコープを小さく保っています — 大きな機能を提案する前に、それが「単一管理者によるセルフホスト」という設計目標に合うかどうかをIssueで議論することを検討してください。

## ライセンス

MIT（`LICENSE` 参照）。
