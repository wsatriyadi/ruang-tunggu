// Package secrets provides AES-GCM encryption for the stream key. The key file
// is stored next to the database but is a separate secret: PRD §10 requires the
// stream key to be encrypted with an application key kept apart from the data.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Box encrypts and decrypts short secrets such as the RTMPS stream key.
type Box struct {
	mu   sync.RWMutex
	aead cipher.AEAD
}

// Load reads the 32-byte key from path, generating one on first use.
func Load(path string) (*Box, error) {
	key, err := loadOrCreateKey(path)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		key, decErr := base64.StdEncoding.DecodeString(string(raw))
		if decErr != nil {
			return nil, fmt.Errorf("decode key file %s: %w", path, decErr)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("key file %s must decode to 32 bytes, got %d", path, len(key))
		}
		return key, nil
	case errors.Is(err, os.ErrNotExist):
		// Create with owner-only permissions before any secret is stored.
		key := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("generate key: %w", err)
		}
		encoded := base64.StdEncoding.EncodeToString(key)
		if err := writeFileExclusive(path, encoded); err != nil {
			return nil, err
		}
		return key, nil
	default:
		return nil, fmt.Errorf("read key file: %w", err)
	}
}

func writeFileExclusive(path, content string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// Lost a race with another process; re-read what it wrote.
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			key, decErr := base64.StdEncoding.DecodeString(string(raw))
			if decErr != nil || len(key) != 32 {
				return fmt.Errorf("key file %s is not a valid 32-byte key", path)
			}
			return nil
		}
		return fmt.Errorf("create key file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	return f.Sync()
}

// Encrypt returns the base64 ciphertext for plaintext.
func (b *Box) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. An empty input yields an empty output.
func (b *Box) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secret payload is truncated")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(plain), nil
}
