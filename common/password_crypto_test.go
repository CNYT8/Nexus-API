package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// These tests deliberately do not use t.Parallel: the serving key is global.
func passwordCryptoTestKey(t *testing.T) *rsa.PublicKey {
	t.Helper()
	passwordEncryptionState.Lock()
	oldPrivate, oldPublic, oldID := passwordEncryptionState.privateKey, passwordEncryptionState.publicKey, passwordEncryptionState.keyID
	passwordEncryptionState.Unlock()
	t.Cleanup(func() {
		passwordEncryptionState.Lock()
		defer passwordEncryptionState.Unlock()
		passwordEncryptionState.privateKey = oldPrivate
		passwordEncryptionState.publicKey = oldPublic
		passwordEncryptionState.keyID = oldID
	})
	key, err := GeneratePasswordEncryptionPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := LoadPasswordEncryptionPrivateKey(key); err != nil {
		t.Fatal(err)
	}
	passwordEncryptionState.RLock()
	defer passwordEncryptionState.RUnlock()
	return &passwordEncryptionState.privateKey.PublicKey
}

func passwordCryptoTestRSA(t *testing.T, publicKey *rsa.PublicKey, plaintext []byte, label string) string {
	t.Helper()
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, publicKey, plaintext, []byte(label))
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func passwordCryptoTestV2(t *testing.T, publicKey *rsa.PublicKey, plaintext, key []byte, label, aad string) string {
	t.Helper()
	if key == nil {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			t.Fatal(err)
		}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	wrapped := passwordCryptoTestRSA(t, publicKey, key, label)
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(aad))
	return strings.Join([]string{"v2", wrapped, base64.StdEncoding.EncodeToString(nonce), base64.StdEncoding.EncodeToString(ciphertext)}, ".")
}

func TestDecryptPasswordRSAAndV2(t *testing.T) {
	publicKey := passwordCryptoTestKey(t)
	kid, _ := PasswordEncryptionPublicKey()
	// U+20000 is a Chinese ideograph encoded in four UTF-8 bytes.
	chinese512 := strings.Repeat("𠀀", 128)
	if len(chinese512) != 512 {
		t.Fatal("fixture must be exactly 512 bytes")
	}
	for _, tc := range []struct {
		name      string
		plaintext string
		v2        bool
	}{
		{"rsa_legacy_short_password", "x", false},
		{"rsa_chinese_password", "中文登录密码", false},
		{"v2_legacy_short_password", "x", true},
		{"v2_chinese_password", "中文登录密码", true},
		{"v2_512_byte_chinese", chinese512, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload string
			if tc.v2 {
				payload = passwordCryptoTestV2(t, publicKey, []byte(tc.plaintext), nil, "password-v2", "password-v2:"+kid)
			} else {
				payload = passwordCryptoTestRSA(t, publicKey, []byte(tc.plaintext), "")
			}
			got, err := DecryptPassword(payload, kid)
			if err != nil || got != tc.plaintext {
				t.Fatal("valid password did not round trip")
			}
		})
	}
}

func TestDecryptPasswordRejectsInvalidPayloadWithoutDisclosure(t *testing.T) {
	publicKey := passwordCryptoTestKey(t)
	kid, _ := PasswordEncryptionPublicKey()
	const secret = "password-secret-must-not-leak"
	rsaPayload := passwordCryptoTestRSA(t, publicKey, []byte(secret), "")
	v2 := func(plaintext []byte) string {
		return passwordCryptoTestV2(t, publicKey, plaintext, nil, "password-v2", "password-v2:"+kid)
	}
	v2Payload := v2([]byte(secret))
	replacePart := func(index int, value string) string {
		parts := strings.Split(v2Payload, ".")
		parts[index] = value
		return strings.Join(parts, ".")
	}
	tamperPart := func(index int) string {
		parts := strings.Split(v2Payload, ".")
		data, err := base64.StdEncoding.DecodeString(parts[index])
		if err != nil {
			t.Fatal(err)
		}
		data[len(data)-1] ^= 1
		return replacePart(index, base64.StdEncoding.EncodeToString(data))
	}
	for _, tc := range []struct {
		name    string
		payload string
		keyID   string
	}{
		{"empty_payload", "", kid},
		{"missing_kid", rsaPayload, ""},
		{"rsa_wrong_kid", rsaPayload, "stale-key-id"},
		{"v2_wrong_kid", v2Payload, "stale-key-id"},
		{"plaintext_never_falls_back", secret, kid},
		{"rsa_broken_base64", "%" + rsaPayload, kid},
		{"rsa_truncated_ciphertext", rsaPayload[4:], kid},
		{"rsa_broken_ciphertext", base64.StdEncoding.EncodeToString(make([]byte, publicKey.Size())), kid},
		{"rsa_wrong_oaep_label", passwordCryptoTestRSA(t, publicKey, []byte(secret), "wrong-label"), kid},
		{"rsa_empty_plaintext", passwordCryptoTestRSA(t, publicKey, nil, ""), kid},
		{"rsa_invalid_utf8", passwordCryptoTestRSA(t, publicKey, []byte{0xff}, ""), kid},
		{"v2_missing_fields", "v2." + rsaPayload, kid},
		{"v2_extra_field", v2Payload + ".extra", kid},
		{"v2_no_rsa_downgrade", "v2." + rsaPayload + "..", kid},
		{"v2_wrapped_key_base64", replacePart(1, "%"), kid},
		{"v2_wrapped_key_size", replacePart(1, "AA=="), kid},
		{"v2_wrapped_key_tamper", tamperPart(1), kid},
		{"v2_nonce_base64", replacePart(2, "%"), kid},
		{"v2_nonce_size", replacePart(2, base64.StdEncoding.EncodeToString(make([]byte, 11))), kid},
		{"v2_nonce_tamper", tamperPart(2), kid},
		{"v2_ciphertext_base64", replacePart(3, "%"), kid},
		{"v2_ciphertext_too_short", replacePart(3, "AA=="), kid},
		{"v2_tag_tamper", tamperPart(3), kid},
		{"v2_wrong_oaep_label", passwordCryptoTestV2(t, publicKey, []byte(secret), nil, "", "password-v2:"+kid), kid},
		{"v2_wrong_aad", passwordCryptoTestV2(t, publicKey, []byte(secret), nil, "password-v2", "password-v2:other-key"), kid},
		{"v2_wrong_aes_key_size", passwordCryptoTestV2(t, publicKey, []byte(secret), make([]byte, 16), "password-v2", "password-v2:"+kid), kid},
		{"v2_empty_plaintext", v2(nil), kid},
		{"v2_invalid_utf8", v2([]byte{0xff}), kid},
		{"v2_513_byte_plaintext", v2([]byte(strings.Repeat("𠀀", 128) + "x")), kid},
		{"oversized_envelope", strings.Repeat("x", maxPasswordEncryptionPayloadLength+1), kid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plaintext, err := DecryptPassword(tc.payload, tc.keyID)
			if plaintext != "" || err != ErrPasswordEncryptionInvalid {
				t.Fatal("invalid input must return no plaintext and the shared public error")
			}
			if err.Error() != "password encryption payload is invalid" || strings.Contains(err.Error(), secret) {
				t.Fatal("invalid input exposed payload details")
			}
		})
	}
}

func TestDecryptPasswordRequiresInitializedKey(t *testing.T) {
	publicKey := passwordCryptoTestKey(t)
	kid, _ := PasswordEncryptionPublicKey()
	payload := passwordCryptoTestRSA(t, publicKey, []byte("old-password"), "")
	passwordEncryptionState.Lock()
	passwordEncryptionState.privateKey = nil
	passwordEncryptionState.publicKey = ""
	passwordEncryptionState.keyID = ""
	passwordEncryptionState.Unlock()
	if plaintext, err := DecryptPassword(payload, kid); plaintext != "" || err != ErrPasswordEncryptionInvalid {
		t.Fatal("uninitialized key must fail closed")
	}
}

func TestDecryptPasswordRejectsStaleKeyAfterReplacement(t *testing.T) {
	publicKey := passwordCryptoTestKey(t)
	oldID, _ := PasswordEncryptionPublicKey()
	const secret = "stale-key-password-must-not-leak"
	payloads := []string{
		passwordCryptoTestRSA(t, publicKey, []byte(secret), ""),
		passwordCryptoTestV2(t, publicKey, []byte(secret), nil, "password-v2", "password-v2:"+oldID),
	}
	newKey, err := GeneratePasswordEncryptionPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := LoadPasswordEncryptionPrivateKey(newKey); err != nil {
		t.Fatal(err)
	}
	newID, _ := PasswordEncryptionPublicKey()
	if oldID == newID {
		t.Fatal("replacement fixture must use a different key")
	}
	for _, payload := range payloads {
		for _, kid := range []string{oldID, newID} {
			// Neither the stale ID nor relabeling with the active ID may
			// expose plaintext from an envelope for the replaced key.
			if plaintext, err := DecryptPassword(payload, kid); plaintext != "" || err != ErrPasswordEncryptionInvalid {
				t.Fatal("stale ciphertext must fail closed without plaintext or crypto details")
			}
		}
	}
}

func TestPasswordEncryptionFailedLoadPreservesActiveKey(t *testing.T) {
	publicKey := passwordCryptoTestKey(t)
	kid, pem := PasswordEncryptionPublicKey()
	payload := passwordCryptoTestRSA(t, publicKey, []byte("existing-password"), "")
	for _, invalid := range []string{"", "not-private-key-pem", pem, "-----BEGIN PRIVATE KEY-----\nAA==\n-----END PRIVATE KEY-----\n"} {
		if err := LoadPasswordEncryptionPrivateKey(invalid); err == nil {
			t.Fatal("invalid private key was accepted")
		}
		gotID, gotPEM := PasswordEncryptionPublicKey()
		if gotID != kid || gotPEM != pem {
			t.Fatal("failed key load changed the serving key")
		}
		if plaintext, err := DecryptPassword(payload, kid); err != nil || plaintext != "existing-password" {
			t.Fatal("failed key load broke existing ciphertext")
		}
	}
}
