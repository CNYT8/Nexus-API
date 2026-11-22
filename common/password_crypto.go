package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	passwordEncryptionKeyBits          = 2048
	maxPasswordEncryptionPayloadLength = 4096
)

var ErrPasswordEncryptionInvalid = errors.New("password encryption payload is invalid")

var passwordEncryptionState struct {
	sync.RWMutex
	privateKey *rsa.PrivateKey
	publicKey  string
	keyID      string
}

// GeneratePasswordEncryptionPrivateKey creates a candidate key. Only the key
// read back from the shared database may be installed in a serving process.
func GeneratePasswordEncryptionPrivateKey() (string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, passwordEncryptionKeyBits)
	if err != nil {
		return "", fmt.Errorf("generate password encryption key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("marshal password encryption key: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

// LoadPasswordEncryptionPrivateKey validates before atomically replacing the
// active key. Failed loads cannot publish a partial or randomly regenerated key.
func LoadPasswordEncryptionPrivateKey(privateKeyPEM string) error {
	block, rest := pem.Decode([]byte(privateKeyPEM))
	if block == nil || block.Type != "PRIVATE KEY" || strings.TrimSpace(string(rest)) != "" {
		return errors.New("password encryption key is not valid PKCS#8 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse password encryption key: %w", err)
	}
	privateKey, ok := parsed.(*rsa.PrivateKey)
	if !ok || privateKey.N == nil || privateKey.N.BitLen() < passwordEncryptionKeyBits {
		return fmt.Errorf("password encryption key must be RSA with at least %d bits", passwordEncryptionKeyBits)
	}
	if err := privateKey.Validate(); err != nil {
		return fmt.Errorf("validate password encryption key: %w", err)
	}
	privateKey.Precompute()
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return fmt.Errorf("marshal password encryption public key: %w", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	digest := sha256.Sum256(der)
	keyID := hex.EncodeToString(digest[:16])

	passwordEncryptionState.Lock()
	defer passwordEncryptionState.Unlock()
	passwordEncryptionState.privateKey = privateKey
	passwordEncryptionState.publicKey = publicKeyPEM
	passwordEncryptionState.keyID = keyID
	return nil
}

// PasswordEncryptionPublicKey exposes only the key ID and public SPKI PEM.
func PasswordEncryptionPublicKey() (keyID string, publicKeyPEM string) {
	passwordEncryptionState.RLock()
	defer passwordEncryptionState.RUnlock()
	return passwordEncryptionState.keyID, passwordEncryptionState.publicKey
}

// DecryptPassword implements upstream RSA-OAEP/SHA-256 and v2 hybrid envelopes.
// V2 wraps an AES-256-GCM key with the password-v2 OAEP label and binds its GCM
// ciphertext to password-v2:<kid>. All invalid inputs share one public error.
func DecryptPassword(payload, keyID string) (string, error) {
	if payload == "" || len(payload) > maxPasswordEncryptionPayloadLength {
		return "", ErrPasswordEncryptionInvalid
	}
	passwordEncryptionState.RLock()
	privateKey := passwordEncryptionState.privateKey
	activeKeyID := passwordEncryptionState.keyID
	passwordEncryptionState.RUnlock()
	if privateKey == nil || keyID == "" || keyID != activeKeyID {
		return "", ErrPasswordEncryptionInvalid
	}

	var plaintext []byte
	if strings.HasPrefix(payload, "v2.") {
		parts := strings.Split(payload, ".")
		if len(parts) != 4 {
			return "", ErrPasswordEncryptionInvalid
		}
		wrappedKey, err := base64.StdEncoding.Strict().DecodeString(parts[1])
		if err != nil || len(wrappedKey) != privateKey.Size() {
			return "", ErrPasswordEncryptionInvalid
		}
		nonce, err := base64.StdEncoding.Strict().DecodeString(parts[2])
		if err != nil || len(nonce) != 12 {
			return "", ErrPasswordEncryptionInvalid
		}
		ciphertext, err := base64.StdEncoding.Strict().DecodeString(parts[3])
		if err != nil || len(ciphertext) <= 16 || len(ciphertext) > MaxAccountPasswordLength*utf8.UTFMax+16 {
			return "", ErrPasswordEncryptionInvalid
		}
		key, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, wrappedKey, []byte("password-v2"))
		if err != nil || len(key) != 32 {
			return "", ErrPasswordEncryptionInvalid
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return "", ErrPasswordEncryptionInvalid
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return "", ErrPasswordEncryptionInvalid
		}
		plaintext, err = gcm.Open(nil, nonce, ciphertext, []byte("password-v2:"+keyID))
		if err != nil {
			return "", ErrPasswordEncryptionInvalid
		}
	} else {
		ciphertext, err := base64.StdEncoding.Strict().DecodeString(payload)
		if err != nil || len(ciphertext) != privateKey.Size() {
			return "", ErrPasswordEncryptionInvalid
		}
		plaintext, err = rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, ciphertext, nil)
		if err != nil {
			return "", ErrPasswordEncryptionInvalid
		}
	}
	// Login retains compatibility with old short passwords; length rules for
	// newly set passwords remain in HashAccountPassword.
	if len(plaintext) == 0 || len(plaintext) > MaxAccountPasswordLength*utf8.UTFMax || !utf8.Valid(plaintext) {
		return "", ErrPasswordEncryptionInvalid
	}
	return string(plaintext), nil
}
