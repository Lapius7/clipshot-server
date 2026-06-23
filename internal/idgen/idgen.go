// Package idgen generates short, URL-safe random IDs with enough entropy
// to make collisions and enumeration impractical.
package idgen

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// New returns a 16-character base62 ID (~95 bits of entropy).
func New() (string, error) {
	const length = 16
	b := make([]byte, length)
	base := big.NewInt(int64(len(alphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, base)
		if err != nil {
			return "", fmt.Errorf("generate id: %w", err)
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}
