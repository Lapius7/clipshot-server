# clipshot-server

[日本語版 README はこちら / Japanese README](README.ja.md)

A self-hostable image upload API server written in Go. It receives images uploaded by [clipshot-app](https://github.com/Lapius7/clipshot-app) (the Windows tray client) and returns a public URL, in the same spirit as ShareX/Gyazo "custom uploader" backends — except you run the whole thing yourself.

- No external dependencies at runtime: images on local disk, metadata in a single SQLite file.
- Ships as a single Docker image; `docker compose up -d` and you have a working instance.
- Token-based auth designed for a single operator handing out keys to their own devices, not a multi-tenant SaaS.

## Table of contents

- [Why this exists](#why-this-exists)
- [Architecture](#architecture)
- [Quick start (Docker Compose)](#quick-start-docker-compose)
- [Quick start (docker run)](#quick-start-docker-run)
- [Running from source](#running-from-source)
- [Configuration reference](#configuration-reference)
- [Token management](#token-management)
- [API reference](#api-reference)
- [Putting it behind HTTPS](#putting-it-behind-https)
- [Data layout & backups](#data-layout--backups)
- [Security model](#security-model)
- [Downloads & verifying releases](#downloads--verifying-releases)
- [Project layout](#project-layout)
- [Roadmap / known limitations](#roadmap--known-limitations)
- [Contributing](#contributing)
- [License](#license)

## Why this exists

Hosted screenshot/image-upload services are convenient until you care about where your images actually live, how long they're kept, or who else can see them. clipshot-server exists so you can point a tiny Windows hotkey tool at a server you control — your own VPS, your home server, whatever — and get the same "press a key, get a URL" workflow without handing your clipboard contents to a third party.

It deliberately does *not* try to be a general-purpose multi-tenant image host. There's no user signup flow, no web dashboard, no billing. It's closer in spirit to a self-hosted ShareX custom uploader endpoint than to a SaaS product.

## Architecture

```
┌─────────────────┐        HTTPS, Bearer token        ┌───────────────────────┐
│  clipshot-app    │ ───────────────────────────────▶  │   clipshot-server     │
│  (Windows tray)  │   POST /api/upload (multipart)    │                       │
└─────────────────┘ ◀───────────────────────────────── │  net/http + SQLite    │
        ▲                  { "url": "https://..." }    │  + local filesystem   │
        │                                               └───────────────────────┘
        │ writes URL to clipboard                                  │
        ▼                                                           ▼
   user pastes URL                                        /data/<id>.<ext>
                                                            /data/clipshot.db
```

Request flow for an upload:

1. Client sends `POST /api/upload` with `Authorization: Bearer <token>` and a `multipart/form-data` body (field name `file`).
2. The auth middleware hashes the presented token (SHA-256) and looks it up in the `tokens` table. Revoked or unknown tokens get `401`.
3. A per-token rate limiter (token bucket, via [`go-rataliy_lib`](https://github.com/Lapius7/go-rataliy_lib)) rejects bursts beyond the configured rate with `429`, setting a `Retry-After` header.
4. The handler enforces `MAX_UPLOAD_MB` via `http.MaxBytesReader`, then **sniffs the real content type from the byte stream** (not the client-supplied `Content-Type` header) and rejects anything outside the png/jpeg/gif/webp allowlist with `415`.
5. A random, high-entropy ID is generated (16 base62 characters, ~95 bits — not sequential, not guessable) and the file is written to `DATA_DIR/<id>.<ext>`.
6. A row is recorded in the `uploads` table (filename, content type, size, owning token, timestamp) for auditability.
7. The server responds `201 Created` with `{"url": "<BASE_URL>/i/<id>.<ext>"}`.

Serving (`GET /i/{id}.{ext}`) is unauthenticated by design — these are meant to be publicly shareable links, just like any other image host. The endpoint sends a long-lived `Cache-Control` header since uploaded image bytes are immutable once written.

## Quick start (Docker Compose)

This is the recommended path for a fresh VPS.

```bash
git clone https://github.com/Lapius7/clipshot-server.git
cd clipshot-server
cp .env.example .env
# edit .env: set BASE_URL to the https:// URL this server will be reachable at,
# e.g. https://img.example.com
docker compose up -d
docker compose logs -f
```

On first boot, if there are zero active tokens in the database, the server automatically mints one and prints it to the logs **exactly once**:

```
===========================================================
No active tokens found. Created a bootstrap token:
cs_f5df7d560471d91eb73dcaa95b56c189177532c6e50e25d36bf79ef781108a20
Save this now -- it will not be shown again. Use it as the
API key in the clipshot-app client (Authorization: Bearer ...)
===========================================================
```

Copy that token into clipshot-app's settings. If you lose it, just [create a new one](#token-management) — there's no way to recover the original plaintext (it's only ever stored hashed).

## Quick start (docker run)

If you'd rather not use Compose:

```bash
docker run -d \
  --name clipshot-server \
  --restart unless-stopped \
  -p 8080:8080 \
  -v $(pwd)/data:/data \
  -e BASE_URL=https://img.example.com \
  ghcr.io/lapius7/clipshot-server:latest
docker logs -f clipshot-server   # grab the bootstrap token on first run
```

> **Note:** the `ghcr.io` image is not published yet — see [Roadmap](#roadmap--known-limitations). Until then, build locally with `docker build -t clipshot-server .` and substitute that tag above.

## Running from source

Requires Go 1.23+. No CGO, no system SQLite — `modernc.org/sqlite` is a pure-Go driver, so this builds and runs anywhere Go does.

```bash
go build -o clipshot-server ./cmd/server
BASE_URL=https://img.example.com DATA_DIR=./data ./clipshot-server
```

Or just `go run ./cmd/server` during development.

## Configuration reference

All configuration is via environment variables (see `.env.example`).

| Variable | Required | Default | Description |
|---|---|---|---|
| `BASE_URL` | ✅ | — | Public base URL used to build returned image links, e.g. `https://img.example.com`. Must match how clients will actually reach the server. |
| `PORT` | | `8080` | TCP port the HTTP server listens on. |
| `DATA_DIR` | | `/data` | Directory for both the SQLite database and uploaded image files. Mount this as a volume. |
| `DB_PATH` | | `<DATA_DIR>/clipshot.db` | Override if you want the DB file somewhere other than `DATA_DIR`. |
| `MAX_UPLOAD_MB` | | `25` | Maximum accepted upload size, in megabytes, enforced before the body is even fully read. |
| `RATE_LIMIT_RPM` | | `30` | Requests per minute allowed per token (steady-state rate). |
| `RATE_LIMIT_BURST` | | `10` | Burst allowance on top of the steady-state rate. |

## Token management

Tokens are how clipshot-app (or anything else speaking the API) authenticates. There is intentionally no self-service signup — an administrator (you) issues tokens via the CLI on the server itself.

```bash
# inside the running container (Compose service name "clipshot-server")
docker compose exec clipshot-server clipshot-server token create -label "desktop-pc"
docker compose exec clipshot-server clipshot-server token create -label "work-laptop"

# revoke a token by its id (the id is logged when you create it, and also
# visible if you query the uploads/tokens table directly)
docker compose exec clipshot-server clipshot-server token revoke -id <token-id>
```

Design notes:

- Tokens are generated with `crypto/rand` (32 random bytes, hex-encoded, prefixed `cs_`).
- Only a SHA-256 hash of the token is ever persisted. The plaintext is shown once at creation time and cannot be retrieved again — if you lose it, revoke it and issue a new one.
- Revocation is soft (sets `revoked_at`), so upload history tied to a revoked token's id is preserved for auditing.
- There's no token expiry built in yet; treat each token as long-lived and revoke it manually if a device is decommissioned or compromised.

## API reference

### `POST /api/upload`

| | |
|---|---|
| Auth | `Authorization: Bearer <token>` (required) |
| Body | `multipart/form-data`, file field name **`file`** |
| Accepted types | image/png, image/jpeg, image/gif, image/webp (detected from file bytes, not the filename or declared MIME type) |
| Success | `201 Created`, `{"url": "https://.../i/<id>.<ext>"}` |
| Errors | `401` no/invalid/revoked token · `400` missing file field · `413` body exceeds `MAX_UPLOAD_MB` · `415` unsupported image type · `429` rate limit exceeded · `500` storage/db failure |

Example:

```bash
curl -X POST https://img.example.com/api/upload \
  -H "Authorization: Bearer cs_xxx..." \
  -F "file=@screenshot.png"
# => {"url":"https://img.example.com/i/EfhLdkk5I36am0pS.png"}
```

### `GET /i/{id}.{ext}`

Serves the stored image bytes directly. No authentication — this is the public, shareable link. Responds `404` if the id/extension pair doesn't exist on disk.

### `GET /healthz`

Returns `200 ok`. Suitable for container healthchecks / uptime monitors.

## Putting it behind HTTPS

clipshot-server itself speaks plain HTTP — it expects a reverse proxy in front of it to terminate TLS. This keeps the Go binary simple and lets you reuse whatever proxy you already run. clipshot-app refuses to talk to anything that isn't `https://`, so this step isn't optional for real use.

Example with [Caddy](https://caddyserver.com/) (automatic HTTPS via Let's Encrypt):

```caddyfile
img.example.com {
    reverse_proxy localhost:8080
}
```

Any reverse proxy works the same way (Nginx, Traefik, etc.) — just forward to whatever `PORT` you configured.

## Data layout & backups

Everything lives under `DATA_DIR` (`/data` in the container, `./data` on the host with the default Compose setup):

```
data/
├── clipshot.db        # SQLite: tokens + upload metadata
├── EfhLdkk5I36am0pS.png
├── 3kQ9z...            .jpg
└── ...
```

Backing up is just backing up this one directory — stop the container, copy `data/`, restart. There's no external database or object store to coordinate.

## Security model

- **Transport**: HTTPS is the operator's responsibility via reverse proxy (see above); the server does not serve TLS itself.
- **Authentication**: opaque bearer tokens, SHA-256 hashed at rest, compared via the standard library's constant-time helpers.
- **Authorization scope**: flat — any valid token can upload. There are no per-token quotas or scoped permissions beyond revocation. This matches the single-operator, multi-device use case (you issuing tokens to your own machines), not a multi-tenant trust boundary.
- **Abuse mitigation**: per-token rate limiting (token bucket) and a hard upload size cap, both server-side and independent of anything the client claims.
- **Content validation**: the server sniffs actual file bytes (`http.DetectContentType`) rather than trusting the client's declared `Content-Type` or filename extension, and only writes files with a server-chosen extension derived from the sniffed type.
- **ID generation**: upload and token IDs use `crypto/rand`, not `math/rand` — IDs are not enumerable or predictable.
- **What this does *not* protect against**: a leaked bearer token grants full upload rights until revoked (there's no per-request scoping or expiry yet — see [Roadmap](#roadmap--known-limitations)). Treat tokens like passwords.

## Downloads & verifying releases

This project does not (yet) publish prebuilt binaries or container images — see [Roadmap](#roadmap--known-limitations). Every [tagged release](https://github.com/Lapius7/clipshot-server/releases) is **source-only**: you build it yourself, either with `docker build`/`docker compose up` or `go build`. There is no separate "official binary" to verify a checksum against, which sidesteps an entire class of "did I download a tampered binary" concerns — but it also means the integrity of what you run depends on the integrity of the source you cloned.

### Verifying the source you cloned

- **Prefer a tagged release over an arbitrary commit.** `git clone` followed by `git checkout v0.1.0` (or whichever tag) gives you a fixed, citable point in history, rather than whatever `main` happens to contain when you clone.
- **Check the tag is what GitHub says it is.** Tags in this repository are not currently GPG-signed (see [Roadmap](#roadmap--known-limitations)), so "verifying" today means comparing against what's shown on the [Releases page](https://github.com/Lapius7/clipshot-server/releases) and the [commit history](https://github.com/Lapius7/clipshot-server/commits/main) on GitHub itself, rather than cryptographic signature verification.
- **Read the Dockerfile before trusting `docker build`.** It's short and has no surprises (multi-stage build, `CGO_ENABLED=0`, runs as a non-root user) — worth a skim since it has access to your build context.

### Why no prebuilt container image yet

Publishing to `ghcr.io` requires a CI pipeline that builds and signs images on every tag, which isn't set up yet (see [Roadmap](#roadmap--known-limitations)). Until then, `docker build -t clipshot-server .` from a tagged checkout is the reproducible equivalent — you're building from the same Dockerfile a CI pipeline would, just on your own machine instead of trusting a registry.

## Project layout

```
cmd/server/main.go        Entry point: config load, bootstrap token, HTTP server startup
internal/config/          Environment variable loading and validation
internal/db/              SQLite connection + schema migration (tokens, uploads tables)
internal/storage/         Storage interface + local-filesystem implementation
internal/auth/            Token creation/verification/revocation (hash-at-rest)
internal/idgen/           Cryptographically random short ID generation
internal/handler/         HTTP routes: upload, serve, healthz
internal/cli/             `clipshot-server token create|revoke` admin subcommands
```

The `storage.Storage` interface is intentionally factored out so a future S3-compatible backend can be added without touching the HTTP handlers — see Roadmap.

## Roadmap / known limitations

This is an early-stage skeleton, not a finished product. Known gaps, roughly in priority order:

- [ ] Publish prebuilt images to `ghcr.io` via CI (currently build-it-yourself only)
- [ ] S3-compatible storage backend behind the existing `storage.Storage` interface
- [ ] Per-token expiry and/or upload quotas
- [ ] Upload deletion endpoint (currently images are permanent once uploaded; manual deletion only via removing the file + DB row)
- [ ] Structured logging / metrics endpoint
- [ ] Automated tests (current verification is manual: build, vet, and a manual end-to-end upload/serve check — see commit history)

## Contributing

Issues and pull requests are welcome. This project intentionally stays small in scope — before proposing a large feature, consider opening an issue first to discuss whether it fits the "single operator, self-hosted" design goal.

## License

MIT (see `LICENSE`).
