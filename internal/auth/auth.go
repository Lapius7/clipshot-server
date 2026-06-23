package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Lapius7/clipshot-server/internal/idgen"
)

var ErrInvalidToken = errors.New("invalid or revoked token")

type Token struct {
	ID    string
	Label string
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateToken generates a new random token, stores its hash, and returns
// the plaintext token. The plaintext is never persisted.
func CreateToken(db *sql.DB, label string) (plaintext string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	plaintext = "cs_" + hex.EncodeToString(raw)

	id, err := idgen.New()
	if err != nil {
		return "", err
	}

	_, err = db.Exec(
		`INSERT INTO tokens (id, token_hash, label, created_at) VALUES (?, ?, ?, ?)`,
		id, hashToken(plaintext), label, time.Now().Unix(),
	)
	if err != nil {
		return "", fmt.Errorf("insert token: %w", err)
	}
	return plaintext, nil
}

// Verify looks up a presented token by its hash and returns the matching
// active (non-revoked) token record.
func Verify(db *sql.DB, presented string) (*Token, error) {
	presented = strings.TrimSpace(presented)
	if presented == "" {
		return nil, ErrInvalidToken
	}
	h := hashToken(presented)

	var t Token
	var revokedAt sql.NullInt64
	err := db.QueryRow(
		`SELECT id, label, revoked_at FROM tokens WHERE token_hash = ?`, h,
	).Scan(&t.ID, &t.Label, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, fmt.Errorf("lookup token: %w", err)
	}
	if revokedAt.Valid {
		return nil, ErrInvalidToken
	}
	// Constant-time confirmation that hash compare was meaningful (defense in depth).
	if subtle.ConstantTimeCompare([]byte(h), []byte(hashToken(presented))) != 1 {
		return nil, ErrInvalidToken
	}
	return &t, nil
}

func RevokeToken(db *sql.DB, id string) error {
	res, err := db.Exec(`UPDATE tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("token not found or already revoked")
	}
	return nil
}

func CountActiveTokens(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE revoked_at IS NULL`).Scan(&n)
	return n, err
}
