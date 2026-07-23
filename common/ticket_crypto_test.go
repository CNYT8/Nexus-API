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
	"strings"
	"testing"
)

func TestTicketContentEncryptionBindsTicketID(t *testing.T) {
	t.Setenv("TICKET_ENCRYPTION_KEY", "ticket-crypto-test-key")

	ciphertext, err := EncryptTicketContent("private ticket content", 41)
	if err != nil {
		t.Fatalf("EncryptTicketContent() error = %v", err)
	}
	if !strings.HasPrefix(ciphertext, "v1:") {
		t.Fatalf("ciphertext is not versioned: %q", ciphertext)
	}

	plaintext, err := DecryptTicketContent(ciphertext, 41)
	if err != nil {
		t.Fatalf("DecryptTicketContent() error = %v", err)
	}
	if plaintext != "private ticket content" {
		t.Fatalf("DecryptTicketContent() = %q", plaintext)
	}
	if _, err := DecryptTicketContent(ciphertext, 42); err == nil {
		t.Fatal("expected decryption with a different ticket ID to fail")
	}
}

func TestTicketContentEncryptionReadsLegacyPlaintext(t *testing.T) {
	plaintext, err := DecryptTicketContent("legacy plaintext", 1)
	if err != nil {
		t.Fatalf("DecryptTicketContent() error = %v", err)
	}
	if plaintext != "legacy plaintext" {
		t.Fatalf("DecryptTicketContent() = %q", plaintext)
	}
}
