// Package shortid generates random, URL-safe short codes (slugs).
package shortid

import (
	"crypto/rand"
	"math/big"
)

// alphabet is base62 — no ambiguous characters removed for now; 62^7 is ~3.5
// trillion combinations, plenty for collision-free random generation.
const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// New returns a cryptographically random base62 slug of length n.
func New(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[idx.Int64()]
	}
	return string(b), nil
}
