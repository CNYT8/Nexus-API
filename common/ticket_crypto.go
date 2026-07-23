/*
Copyright (C) 2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

package common

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

const ticketCipherVersion = "v1"

var (
	ticketEncryptionKey   string
	ticketEncryptionKeyMu sync.RWMutex
)

// SetTicketEncryptionKey configures the database-persisted fallback used when
// TICKET_ENCRYPTION_KEY is not explicitly provided by the deployment.
func SetTicketEncryptionKey(value string) {
	ticketEncryptionKeyMu.Lock()
	ticketEncryptionKey = strings.TrimSpace(value)
	ticketEncryptionKeyMu.Unlock()
}

func getTicketEncryptionKey() string {
	ticketEncryptionKeyMu.RLock()
	value := ticketEncryptionKey
	ticketEncryptionKeyMu.RUnlock()
	return value
}

func ticketCipher() (cipher.AEAD, error) {
	keyMaterial := strings.TrimSpace(os.Getenv("TICKET_ENCRYPTION_KEY"))
	if keyMaterial == "" {
		keyMaterial = getTicketEncryptionKey()
	}
	if keyMaterial == "" {
		return nil, errors.New("ticket encryption key is not initialized")
	}
	key := sha256.Sum256([]byte(keyMaterial))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// EncryptTicketContent protects message bodies while keeping the schema
// portable across MySQL, PostgreSQL, and SQLite.
func EncryptTicketContent(content string, ticketID int) (string, error) {
	aead, err := ticketCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := crand.Read(nonce); err != nil {
		return "", err
	}
	aad := []byte(fmt.Sprintf("nexus-ticket:%d", ticketID))
	ciphertext := aead.Seal(nil, nonce, []byte(content), aad)
	payload := append(nonce, ciphertext...)
	return ticketCipherVersion + ":" + base64.RawStdEncoding.EncodeToString(payload), nil
}

func DecryptTicketContent(value string, ticketID int) (string, error) {
	if len(value) < len(ticketCipherVersion)+1 || value[:len(ticketCipherVersion)+1] != ticketCipherVersion+":" {
		// Keep backward compatibility with any manually created plaintext rows.
		return value, nil
	}
	aead, err := ticketCipher()
	if err != nil {
		return "", err
	}
	payload, err := base64.RawStdEncoding.DecodeString(value[len(ticketCipherVersion)+1:])
	if err != nil {
		return "", err
	}
	if len(payload) < aead.NonceSize() {
		return "", errors.New("invalid ticket ciphertext")
	}
	nonce, ciphertext := payload[:aead.NonceSize()], payload[aead.NonceSize():]
	aad := []byte(fmt.Sprintf("nexus-ticket:%d", ticketID))
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
