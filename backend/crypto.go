package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ---------- secrets at rest (AES-256-GCM) ----------

// Sealer encrypts small secrets such as a workspace's Anthropic API key.
// The additional data binds a ciphertext to its owner, so a value copied to
// another workspace's row will not decrypt.
type Sealer struct{ aead cipher.AEAD }

func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("the secret key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// ParseSecretKey accepts 32 bytes as base64 or 64 hex characters.
func ParseSecretKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("SECRET_KEY must be 32 random bytes, as base64 or hex (generate one with: openssl rand -base64 32)")
}

func (s *Sealer) Seal(plain, aad string) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // the system random source failing is not recoverable
	}
	return s.aead.Seal(nonce, nonce, []byte(plain), []byte(aad))
}

func (s *Sealer) Open(data []byte, aad string) (string, error) {
	n := s.aead.NonceSize()
	if len(data) < n+s.aead.Overhead() {
		return "", errors.New("stored secret is corrupt")
	}
	plain, err := s.aead.Open(nil, data[:n], data[n:], []byte(aad))
	if err != nil {
		return "", errors.New("stored secret cannot be decrypted (wrong SECRET_KEY?)")
	}
	return string(plain), nil
}

// ---------- passwords (PBKDF2-HMAC-SHA256) ----------

const pbkdf2Iterations = 600_000 // OWASP guidance for PBKDF2-HMAC-SHA256

func hashPassword(password string) (string, error) {
	return hashPasswordN(password, pbkdf2Iterations)
}

func hashPasswordN(password string, iter int) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, iter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iter,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(dk)), nil
}

func checkPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > 5_000_000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---------- tokens ----------

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
