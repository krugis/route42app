package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/krugis/route42app/internal/llm"
)

// keyCipher encrypts provider API keys at rest with AES-256-GCM. The key
// comes from ROUTE42_ENCRYPTION_KEY (any string, SHA-256 derived) or,
// by default, a random 32-byte keyfile created next to the database with
// owner-only permissions. There is deliberately no built-in fallback key.
type keyCipher struct {
	key []byte
}

const keyFileName = "route42.key"

func newKeyCipher(dir string) (*keyCipher, error) {
	if env := os.Getenv("ROUTE42_ENCRYPTION_KEY"); env != "" {
		sum := sha256.Sum256([]byte(env))
		return &keyCipher{key: sum[:]}, nil
	}

	path := filepath.Join(dir, keyFileName)
	if raw, err := os.ReadFile(path); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("keyfile %s is corrupt; restore it or delete it to reset stored provider keys", path)
		}
		return &keyCipher{key: key}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read keyfile: %w", err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("write keyfile: %w", err)
	}
	return &keyCipher{key: key}, nil
}

func (c *keyCipher) encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *keyCipher) decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, sealed := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt provider key: %w", err)
	}
	return string(plain), nil
}

// SetProviderKey stores (or replaces) a provider API key, encrypted.
// The provider name is canonicalized (aliases accepted).
func (s *Store) SetProviderKey(provider, apiKey string) error {
	canonical := llm.CanonicalProvider(provider)
	if canonical == "" {
		return errors.New("provider name required")
	}
	if apiKey == "" {
		return errors.New("api key required (use DeleteProviderKey to remove)")
	}
	enc, err := s.cipher.encrypt(apiKey)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`INSERT INTO provider_keys (provider, api_key_enc, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(provider) DO UPDATE SET api_key_enc = excluded.api_key_enc, updated_at = excluded.updated_at`,
		canonical, enc, now, now)
	return err
}

// GetProviderKey returns the decrypted key for a provider, or "" when
// none is stored.
func (s *Store) GetProviderKey(provider string) (string, error) {
	var enc string
	err := s.db.QueryRow(`SELECT api_key_enc FROM provider_keys WHERE provider = ?`,
		llm.CanonicalProvider(provider)).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return s.cipher.decrypt(enc)
}

// DeleteProviderKey removes a stored key. Deleting a missing key is not
// an error.
func (s *Store) DeleteProviderKey(provider string) error {
	_, err := s.db.Exec(`DELETE FROM provider_keys WHERE provider = ?`, llm.CanonicalProvider(provider))
	return err
}

// ListProviders returns canonical names of providers with stored keys.
func (s *Store) ListProviders() ([]string, error) {
	rows, err := s.db.Query(`SELECT provider FROM provider_keys ORDER BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
