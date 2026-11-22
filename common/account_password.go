package common

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinAccountPasswordLength = 8
	MaxAccountPasswordLength = 128
)

var ErrAccountPasswordLength = errors.New("密码长度必须为 8–128 个字符")

func ValidateNewAccountPassword(password string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < MinAccountPasswordLength || utf8.RuneCountInString(password) > MaxAccountPasswordLength {
		return ErrAccountPasswordLength
	}
	return nil
}

// Deploy dual readers everywhere before opting in to Argon2id writes. This
// account-only policy deliberately leaves MFA backup-code hashing unchanged.
func HashAccountPassword(password string) (string, error) {
	if err := ValidateNewAccountPassword(password); err != nil {
		return "", err
	}
	switch os.Getenv("ACCOUNT_PASSWORD_HASH_ALGORITHM") {
	case "", "bcrypt":
		if len(password) > 72 {
			return "", errors.New("密码存储升级完成前不支持超过 72 字节的密码")
		}
		return Password2Hash(password)
	case "argon2id":
	default:
		return "", errors.New("unsupported account password hashing configuration")
	}
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt[:], 2, 19456, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt[:]), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyAccountPassword(password, encoded string) bool {
	if !strings.HasPrefix(encoded, "$argon2id$") {
		return ValidatePasswordAndHash(password, encoded)
	}
	if len(password) > MaxAccountPasswordLength*utf8.UTFMax || len(encoded) > 256 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func AccountPasswordNeedsUpgrade(encoded string) bool {
	return os.Getenv("ACCOUNT_PASSWORD_HASH_ALGORITHM") == "argon2id" && strings.HasPrefix(encoded, "$2")
}
