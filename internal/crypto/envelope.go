package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type Envelope struct {
	KeyID      string `json:"key_id"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type Manager struct {
	currentKeyID string
	keys         map[string][]byte
}

func NewManager(currentKeyID string, keys map[string][]byte) (*Manager, error) {
	if currentKeyID == "" {
		return nil, fmt.Errorf("current key id is empty")
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("keys map is empty")
	}
	if _, ok := keys[currentKeyID]; !ok {
		return nil, fmt.Errorf("current key id %q not found", currentKeyID)
	}
	for id, key := range keys {
		if len(key) != 32 {
			return nil, fmt.Errorf("key %q must be 32 bytes", id)
		}
	}
	cp := make(map[string][]byte, len(keys))
	for id, key := range keys {
		buf := make([]byte, len(key))
		copy(buf, key)
		cp[id] = buf
	}
	return &Manager{currentKeyID: currentKeyID, keys: cp}, nil
}

func (m *Manager) Encrypt(plaintext []byte) (Envelope, error) {
	key := m.keys[m.currentKeyID]
	block, err := aes.NewCipher(key)
	if err != nil {
		return Envelope{}, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Envelope{}, fmt.Errorf("new gcm: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, fmt.Errorf("nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, nil)

	return Envelope{
		KeyID:      m.currentKeyID,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

func (m *Manager) Decrypt(env Envelope) ([]byte, error) {
	key, ok := m.keys[env.KeyID]
	if !ok {
		return nil, fmt.Errorf("unknown key id %q", env.KeyID)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

func (m *Manager) MarshalEncryptedString(value string) (string, error) {
	env, err := m.Encrypt([]byte(value))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("marshal envelope: %w", err)
	}
	return string(b), nil
}

func (m *Manager) UnmarshalEncryptedString(raw string) (string, error) {
	var env Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return "", fmt.Errorf("unmarshal envelope: %w", err)
	}
	pt, err := m.Decrypt(env)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func (m *Manager) ReEncrypt(raw string) (string, error) {
	plain, err := m.UnmarshalEncryptedString(raw)
	if err != nil {
		return "", err
	}
	return m.MarshalEncryptedString(plain)
}
