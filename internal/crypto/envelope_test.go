package crypto

import (
	"encoding/base64"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	keys := map[string][]byte{
		"k1": mustKey(t, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="),
	}
	m, err := NewManager("k1", keys)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	raw, err := m.MarshalEncryptedString("super-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	out, err := m.UnmarshalEncryptedString(raw)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if out != "super-secret" {
		t.Fatalf("expected original string, got %q", out)
	}
}

func TestRotationDecryptOldEncryptNew(t *testing.T) {
	oldKey := mustKey(t, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	newKey := mustKey(t, "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")

	oldManager, err := NewManager("old", map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatalf("old manager: %v", err)
	}
	oldCipher, err := oldManager.MarshalEncryptedString("legacy")
	if err != nil {
		t.Fatalf("old encrypt: %v", err)
	}

	rotatedManager, err := NewManager("new", map[string][]byte{
		"old": oldKey,
		"new": newKey,
	})
	if err != nil {
		t.Fatalf("rotated manager: %v", err)
	}

	plain, err := rotatedManager.UnmarshalEncryptedString(oldCipher)
	if err != nil {
		t.Fatalf("decrypt with old key failed: %v", err)
	}
	if plain != "legacy" {
		t.Fatalf("unexpected plaintext: %q", plain)
	}

	newCipher, err := rotatedManager.MarshalEncryptedString("fresh")
	if err != nil {
		t.Fatalf("new encrypt failed: %v", err)
	}
	fresh, err := rotatedManager.UnmarshalEncryptedString(newCipher)
	if err != nil {
		t.Fatalf("new decrypt failed: %v", err)
	}
	if fresh != "fresh" {
		t.Fatalf("unexpected new plaintext: %q", fresh)
	}
}

func mustKey(t *testing.T, b64 string) []byte {
	t.Helper()
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	if len(k) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(k))
	}
	return k
}
