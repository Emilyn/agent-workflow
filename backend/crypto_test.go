package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSealerRoundTripAndBinding(t *testing.T) {
	s, err := NewSealer(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc := s.Seal("sk-ant-secret-value", "s123")
	if bytes.Contains(enc, []byte("secret-value")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	if got, err := s.Open(enc, "s123"); err != nil || got != "sk-ant-secret-value" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	if _, err := s.Open(enc, "s999"); err == nil {
		t.Error("a value sealed for one workspace opened for another")
	}
	enc[len(enc)-1] ^= 1
	if _, err := s.Open(enc, "s123"); err == nil {
		t.Error("tampered ciphertext opened")
	}
	if _, err := s.Open([]byte("short"), "s123"); err == nil {
		t.Error("short input opened")
	}
	other, _ := NewSealer(bytes.Repeat([]byte{2}, 32))
	if _, err := other.Open(s.Seal("x", "a"), "a"); err == nil {
		t.Error("a different SECRET_KEY opened the value")
	}
	if a, b := s.Seal("same", "a"), s.Seal("same", "a"); bytes.Equal(a, b) {
		t.Error("nonce reused")
	}
	if _, err := NewSealer(make([]byte, 16)); err == nil {
		t.Error("accepted a 16-byte key")
	}
}

func TestParseSecretKey(t *testing.T) {
	hexKey := strings.Repeat("ab", 32)
	if k, err := ParseSecretKey(hexKey); err != nil || len(k) != 32 {
		t.Errorf("hex: %v", err)
	}
	b64 := "q83vq83vq83vq83vq83vq83vq83vq83vq83vq83vq80="
	if k, err := ParseSecretKey(b64); err != nil || len(k) != 32 {
		t.Errorf("base64: %v %d", err, len(k))
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 63)} {
		if _, err := ParseSecretKey(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestPasswordHashing(t *testing.T) {
	h, err := hashPasswordN("correct horse", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h, "correct horse") {
		t.Fatal("hash contains the password")
	}
	if !checkPassword("correct horse", h) || checkPassword("wrong", h) || checkPassword("", h) {
		t.Error("password check is wrong")
	}
	h2, _ := hashPasswordN("correct horse", 1000)
	if h == h2 {
		t.Error("salt reused")
	}
	for _, bad := range []string{"", "x", "pbkdf2-sha256$0$aa$bb", "pbkdf2-sha256$99999999$aa$bb", "md5$1$aa$bb"} {
		if checkPassword("correct horse", bad) {
			t.Errorf("accepted malformed hash %q", bad)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(3, 1<<62)
	for i := 0; i < 3; i++ {
		if !l.allow("a") {
			t.Fatal("blocked too early")
		}
	}
	if l.allow("a") {
		t.Error("allowed past the limit")
	}
	if !l.allow("b") {
		t.Error("limits are not per key")
	}
}
