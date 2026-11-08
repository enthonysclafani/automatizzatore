// Package vault seals values with the master key (AES-256-GCM).
//
// The master key comes from PENDOLO_MASTER_KEY, from a key file, or, the
// first time Pendolo starts without either, is generated into the data
// folder. Whoever holds the database and the key can read every secret, so
// keep the key apart from the database backups.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeySize is the length of the master key in bytes.
const KeySize = 32

// version prefixes sealed values, so the format can change later.
const version = "v1:"

// Vault seals and opens values.
type Vault struct {
	aead cipher.AEAD
}

// New returns a vault for a 32-byte key.
func New(key []byte) (*Vault, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// Seal encrypts plaintext. context binds the value to where it is stored
// (e.g. "secret:<workspace>:<name>"): a value copied elsewhere will not open.
func (v *Vault) Seal(plaintext []byte, context string) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := v.aead.Seal(nonce, nonce, plaintext, []byte(context))
	return version + base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value sealed with the same context.
func (v *Vault) Open(sealed, context string) ([]byte, error) {
	raw, ok := strings.CutPrefix(sealed, version)
	if !ok {
		return nil, errors.New("unknown sealed value format")
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	n := v.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("sealed value too short")
	}
	plain, err := v.aead.Open(nil, data[:n], data[n:], []byte(context))
	if err != nil {
		return nil, errors.New("cannot decrypt: wrong master key or tampered value")
	}
	return plain, nil
}

// KeySource says where LoadKey found the key.
type KeySource string

// Key sources.
const (
	FromEnv       KeySource = "environment"
	FromFile      KeySource = "file"
	FromGenerated KeySource = "generated"
)

// LoadKey reads the master key from envValue (base64), then keyFile, then
// dataDir/master.key, generating that file when nothing exists yet.
func LoadKey(envValue, keyFile, dataDir string) ([]byte, KeySource, error) {
	if envValue != "" {
		key, err := decodeKey(envValue)
		if err != nil {
			return nil, "", fmt.Errorf("PENDOLO_MASTER_KEY: %w", err)
		}
		return key, FromEnv, nil
	}
	if keyFile != "" {
		if b, err := os.ReadFile(keyFile); err == nil {
			key, err := decodeKey(string(b))
			if err != nil {
				return nil, "", fmt.Errorf("%s: %w", keyFile, err)
			}
			return key, FromFile, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, "", err
		}
	}
	path := filepath.Join(dataDir, "master.key")
	if b, err := os.ReadFile(path); err == nil {
		key, err := decodeKey(string(b))
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", path, err)
		}
		return key, FromFile, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, "", fmt.Errorf("save generated master key: %w", err)
	}
	return key, FromGenerated, nil
}

// NewKey returns a random key, base64-encoded, for `pendolo keygen`.
func NewKey() (string, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func decodeKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("not valid base64; create one with `pendolo keygen`")
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("must decode to %d bytes, got %d; create one with `pendolo keygen`", KeySize, len(key))
	}
	return key, nil
}
