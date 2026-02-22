// Package crypto provides key management, EIP-712 signing, and HMAC
// authentication for the Polymarket CLOB API.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	// pbkdf2Iterations is the OWASP-recommended minimum for HMAC-SHA256.
	pbkdf2Iterations = 480_000
	// saltLen is the random salt length in bytes.
	saltLen = 16
	// aesKeyLen is the derived AES-256 key length.
	aesKeyLen = 32
	// currentVersion is the encrypted-key JSON schema version.
	currentVersion = 1
)

// encryptedKeyJSON is the on-disk format for an encrypted private key.
type encryptedKeyJSON struct {
	Version    int    `json:"version"`
	Salt       string `json:"salt"`       // base64 standard encoding
	Nonce      string `json:"nonce"`      // base64 standard encoding
	Ciphertext string `json:"ciphertext"` // base64 standard encoding
}

// KeyConfig carries the information LoadKey needs to resolve a private key.
// Populate the fields from environment variables or a config file.
type KeyConfig struct {
	// RawPrivateKey is the hex-encoded private key (with or without 0x prefix).
	// If non-empty, LoadKey returns it directly.
	RawPrivateKey string

	// EncryptedKeyPath is the path to a JSON file produced by EncryptKey.
	EncryptedKeyPath string

	// KeyPassword is the password used to decrypt the file at EncryptedKeyPath.
	KeyPassword string
}

// EncryptKey encrypts a hex-encoded private key with a password using
// PBKDF2-HMAC-SHA256 key derivation and AES-256-GCM authenticated encryption.
// It returns the JSON blob suitable for writing to disk.
func EncryptKey(privateKeyHex string, password string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("crypto: password must not be empty")
	}

	// Normalise the key: strip optional 0x prefix and validate hex.
	keyHex := strings.TrimPrefix(privateKeyHex, "0x")
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("crypto: invalid private key hex: %w", err)
	}
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("crypto: expected 32-byte key, got %d bytes", len(keyBytes))
	}

	// Generate random salt and derive AES key.
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("crypto: generating salt: %w", err)
	}

	derivedKey := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, aesKeyLen, sha256.New)

	// AES-256-GCM encrypt.
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("crypto: creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: creating GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: generating nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, keyBytes, nil)

	out := encryptedKeyJSON{
		Version:    currentVersion,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}

	return json.MarshalIndent(out, "", "  ")
}

// DecryptKey decrypts a JSON blob produced by EncryptKey, returning the
// hex-encoded private key (without 0x prefix).
func DecryptKey(encryptedJSON []byte, password string) (string, error) {
	if password == "" {
		return "", errors.New("crypto: password must not be empty")
	}

	var stored encryptedKeyJSON
	if err := json.Unmarshal(encryptedJSON, &stored); err != nil {
		return "", fmt.Errorf("crypto: parsing encrypted key JSON: %w", err)
	}
	if stored.Version != currentVersion {
		return "", fmt.Errorf("crypto: unsupported version %d", stored.Version)
	}

	salt, err := base64.StdEncoding.DecodeString(stored.Salt)
	if err != nil {
		return "", fmt.Errorf("crypto: decoding salt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(stored.Nonce)
	if err != nil {
		return "", fmt.Errorf("crypto: decoding nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(stored.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("crypto: decoding ciphertext: %w", err)
	}

	derivedKey := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, aesKeyLen, sha256.New)

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return "", fmt.Errorf("crypto: creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: creating GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decryption failed (wrong password?): %w", err)
	}

	return hex.EncodeToString(plaintext), nil
}

// LoadKey resolves a private key from the provided configuration.
//
// Resolution order:
//  1. If RawPrivateKey is set, return it (stripping 0x prefix).
//  2. If EncryptedKeyPath is set, read the file and decrypt with KeyPassword.
//  3. Otherwise, return an error.
func LoadKey(cfg KeyConfig) (string, error) {
	// 1. Raw key takes precedence.
	if cfg.RawPrivateKey != "" {
		k := strings.TrimPrefix(cfg.RawPrivateKey, "0x")
		if _, err := hex.DecodeString(k); err != nil {
			return "", fmt.Errorf("crypto: RawPrivateKey is not valid hex: %w", err)
		}
		return k, nil
	}

	// 2. Encrypted key file.
	if cfg.EncryptedKeyPath != "" {
		data, err := os.ReadFile(cfg.EncryptedKeyPath)
		if err != nil {
			return "", fmt.Errorf("crypto: reading encrypted key file: %w", err)
		}
		return DecryptKey(data, cfg.KeyPassword)
	}

	return "", errors.New("crypto: no private key source configured (set RawPrivateKey or EncryptedKeyPath)")
}
