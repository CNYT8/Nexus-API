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

package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func TestInitializeTicketEncryptionSecretPersistsIndependentValue(t *testing.T) {
	t.Setenv("TICKET_ENCRYPTION_KEY", "")
	truncateTables(t)
	if err := DB.Where("key = ?", ticketEncryptionSecretOptionKey).Delete(&Option{}).Error; err != nil {
		t.Fatalf("delete existing ticket secret error = %v", err)
	}

	originalCryptoSecret := common.CryptoSecret
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	defer func() {
		common.CryptoSecret = originalCryptoSecret
		common.SetTicketEncryptionKey("")
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	}()

	common.CryptoSecret = "first-process-secret"
	common.SetTicketEncryptionKey("")
	if err := initializeTicketEncryptionSecret(); err != nil {
		t.Fatalf("initializeTicketEncryptionSecret() error = %v", err)
	}

	var option Option
	if err := DB.First(&option, "key = ?", ticketEncryptionSecretOptionKey).Error; err != nil {
		t.Fatalf("read ticket encryption secret error = %v", err)
	}
	if option.Value == "" || option.Value == "first-process-secret" {
		t.Fatalf("persisted ticket secret must be independent from CryptoSecret")
	}
	persistedSecret := option.Value

	common.CryptoSecret = "second-process-secret"
	common.SetTicketEncryptionKey("")
	if err := initializeTicketEncryptionSecret(); err != nil {
		t.Fatalf("second initializeTicketEncryptionSecret() error = %v", err)
	}
	if err := DB.First(&option, "key = ?", ticketEncryptionSecretOptionKey).Error; err != nil {
		t.Fatalf("read persisted ticket secret error = %v", err)
	}
	if option.Value != persistedSecret {
		t.Fatalf("ticket secret changed across initialization")
	}

	ciphertext, err := common.EncryptTicketContent("persistent", 7)
	if err != nil {
		t.Fatalf("EncryptTicketContent() error = %v", err)
	}
	plaintext, err := common.DecryptTicketContent(ciphertext, 7)
	if err != nil || plaintext != "persistent" {
		t.Fatalf("DecryptTicketContent() = %q, %v", plaintext, err)
	}
}

func TestInitializeTicketEncryptionSecretKeepsLegacyEncryptedRowsReadable(t *testing.T) {
	t.Setenv("TICKET_ENCRYPTION_KEY", "")
	truncateTables(t)

	originalCryptoSecret := common.CryptoSecret
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	common.CryptoSecret = "legacy-ticket-secret"
	common.SetTicketEncryptionKey(common.CryptoSecret)
	t.Cleanup(func() {
		common.CryptoSecret = originalCryptoSecret
		common.SetTicketEncryptionKey("")
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	ciphertext, err := common.EncryptTicketContent("legacy encrypted ticket", 7101)
	if err != nil {
		t.Fatalf("EncryptTicketContent() error = %v", err)
	}
	ticket := Ticket{Id: 7101, UserId: 9101, Type: TicketTypeOther, Status: TicketStatusPending, LastAuthor: TicketAuthorUser}
	if err := DB.Create(&ticket).Error; err != nil {
		t.Fatalf("create legacy ticket error = %v", err)
	}
	message := TicketMessage{TicketId: ticket.Id, AuthorId: ticket.UserId, AuthorRole: TicketAuthorUser, Content: ciphertext}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("create legacy ticket message error = %v", err)
	}

	common.SetTicketEncryptionKey("")
	if err := initializeTicketEncryptionSecret(); err != nil {
		t.Fatalf("initializeTicketEncryptionSecret() error = %v", err)
	}
	plaintext, err := common.DecryptTicketContent(ciphertext, ticket.Id)
	if err != nil || plaintext != "legacy encrypted ticket" {
		t.Fatalf("DecryptTicketContent() = %q, %v", plaintext, err)
	}
}

func TestTicketEncryptionSecretRejectsOptionKeyVariants(t *testing.T) {
	for _, key := range []string{
		"TicketEncryptionSecret",
		"ticketencryptionsecret",
		" TicketEncryptionSecret ",
	} {
		if err := validateOptionBeforeSave(key, "replacement"); err == nil {
			t.Fatalf("validateOptionBeforeSave(%q) accepted a protected key", key)
		}
	}
}

func TestTicketReplyOwnershipAndStateTransitions(t *testing.T) {
	t.Setenv("TICKET_ENCRYPTION_KEY", "model-ticket-test-key")
	truncateTables(t)
	if err := DB.Create(&User{Id: 901, Username: "ticket-owner", Status: common.UserStatusEnabled, Group: "default"}).Error; err != nil {
		t.Fatalf("create user error = %v", err)
	}

	ticket, err := CreateTicket(901, TicketTypeTechnical, "The request timed out")
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if ticket.Status != TicketStatusPending || ticket.HasAdminReply {
		t.Fatalf("unexpected initial ticket state: %#v", ticket)
	}
	if _, err := AddTicketReply(ticket.Id, 902, TicketAuthorUser, "I should not be able to reply"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected foreign user reply to be rejected, got %v", err)
	}

	adminReply, err := AddTicketReply(ticket.Id, 10, TicketAuthorAdmin, "We are investigating")
	if err != nil {
		t.Fatalf("admin AddTicketReply() error = %v", err)
	}
	if adminReply.Status != TicketStatusReplied || !adminReply.HasAdminReply || adminReply.LastAuthor != TicketAuthorAdmin {
		t.Fatalf("unexpected administrator reply state: %#v", adminReply)
	}

	userReply, err := AddTicketReply(ticket.Id, 901, TicketAuthorUser, "Thank you")
	if err != nil {
		t.Fatalf("user AddTicketReply() error = %v", err)
	}
	if userReply.Status != TicketStatusPending || !userReply.HasAdminReply || userReply.LastAuthor != TicketAuthorUser {
		t.Fatalf("unexpected user follow-up state: %#v", userReply)
	}

	if _, err := SetTicketStatus(ticket.Id, TicketStatusClosed); err != nil {
		t.Fatalf("SetTicketStatus() error = %v", err)
	}
	if _, err := AddTicketReply(ticket.Id, 901, TicketAuthorUser, "Please reopen"); !errors.Is(err, ErrTicketClosed) {
		t.Fatalf("expected closed ticket reply to be rejected, got %v", err)
	}
}

func TestTicketListsReturnOnlyMetadata(t *testing.T) {
	t.Setenv("TICKET_ENCRYPTION_KEY", "model-ticket-test-key")
	truncateTables(t)

	if err := DB.Create(&User{Id: 903, Username: "ticket-owner", Status: common.UserStatusEnabled, Group: "default"}).Error; err != nil {
		t.Fatalf("create user error = %v", err)
	}
	if _, err := CreateTicket(903, TicketTypeFinance, "I need billing help"); err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	pageInfo := &common.PageInfo{Page: 1, PageSize: 20}
	items, total, err := ListTicketsByUser(903, pageInfo)
	if err != nil {
		t.Fatalf("ListTicketsByUser() error = %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Messages != nil {
		t.Fatalf("unexpected user ticket list: total=%d items=%#v", total, items)
	}

	adminItems, total, err := ListTicketsForAdmin("", pageInfo)
	if err != nil {
		t.Fatalf("ListTicketsForAdmin() error = %v", err)
	}
	if total != 1 || len(adminItems) != 1 || adminItems[0].Username != "ticket-owner" || adminItems[0].Messages != nil {
		t.Fatalf("unexpected admin ticket list: total=%d items=%#v", total, adminItems)
	}
}

func TestTicketResourceLimits(t *testing.T) {
	t.Setenv("TICKET_ENCRYPTION_KEY", "model-ticket-test-key")
	truncateTables(t)

	user := User{Id: 904, Username: "ticket-limit-owner", Status: common.UserStatusEnabled, Group: "default"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user error = %v", err)
	}
	var firstTicket *TicketView
	for i := 0; i < MaxOpenTicketsPerUser; i++ {
		ticket, err := CreateTicket(user.Id, TicketTypeOther, "Need support")
		if err != nil {
			t.Fatalf("CreateTicket(%d) error = %v", i, err)
		}
		if firstTicket == nil {
			firstTicket = ticket
		}
	}
	if _, err := CreateTicket(user.Id, TicketTypeOther, "One too many"); !errors.Is(err, ErrTicketOpenLimit) {
		t.Fatalf("expected open ticket limit, got %v", err)
	}
	if _, err := SetTicketStatus(firstTicket.Id, TicketStatusClosed); err != nil {
		t.Fatalf("SetTicketStatus() error = %v", err)
	}
	replacement, err := CreateTicket(user.Id, TicketTypeOther, "Replacement ticket")
	if err != nil {
		t.Fatalf("CreateTicket() after close error = %v", err)
	}

	var ticket Ticket
	if err := DB.First(&ticket, "id = ?", replacement.Id).Error; err != nil {
		t.Fatalf("read ticket error = %v", err)
	}
	messages := make([]TicketMessage, 0, MaxMessagesPerTicket-1)
	for i := 1; i < MaxMessagesPerTicket; i++ {
		messages = append(messages, TicketMessage{
			TicketId:   ticket.Id,
			AuthorId:   user.Id,
			AuthorRole: TicketAuthorUser,
			Content:    "legacy plaintext",
		})
	}
	if err := DB.Create(&messages).Error; err != nil {
		t.Fatalf("create ticket messages error = %v", err)
	}
	if _, err := AddTicketReply(ticket.Id, user.Id, TicketAuthorUser, "One too many"); !errors.Is(err, ErrTicketMessageLimit) {
		t.Fatalf("expected ticket message limit, got %v", err)
	}
}
