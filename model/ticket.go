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
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ticket_setting"
	"gorm.io/gorm"
)

const (
	TicketTypeFinance   = "finance"
	TicketTypeTechnical = "technical"
	TicketTypeOther     = "other"

	TicketStatusPending = "pending"
	TicketStatusReplied = "replied"
	TicketStatusClosed  = "closed"

	TicketAuthorUser  = "user"
	TicketAuthorAdmin = "admin"

	MaxOpenTicketsPerUser = 20
	MaxMessagesPerTicket  = 100
)

var validTicketTypes = map[string]struct{}{
	TicketTypeFinance:   {},
	TicketTypeTechnical: {},
	TicketTypeOther:     {},
}

var validTicketStatuses = map[string]struct{}{
	TicketStatusPending: {},
	TicketStatusReplied: {},
	TicketStatusClosed:  {},
}

var (
	ErrTicketContentRequired = errors.New("ticket content is required")
	ErrTicketContentTooLong  = errors.New("ticket content is too long")
	ErrTicketInvalidType     = errors.New("invalid ticket type")
	ErrTicketInvalidStatus   = errors.New("invalid ticket status")
	ErrTicketInvalidAuthor   = errors.New("invalid ticket author role")
	ErrTicketClosed          = errors.New("closed ticket cannot receive replies")
	ErrTicketOpenLimit       = errors.New("too many open tickets")
	ErrTicketMessageLimit    = errors.New("too many ticket messages")
)

type Ticket struct {
	Id            int            `json:"id" gorm:"primaryKey"`
	UserId        int            `json:"user_id" gorm:"not null;index:idx_nexus_ticket_user_status,priority:1;index:idx_nexus_ticket_user_updated,priority:1"`
	Type          string         `json:"type" gorm:"type:varchar(32);not null;index"`
	Status        string         `json:"status" gorm:"type:varchar(16);not null;default:'pending';index:idx_nexus_ticket_user_status,priority:2;index:idx_nexus_ticket_status_updated,priority:1"`
	LastAuthor    string         `json:"last_author" gorm:"type:varchar(16);not null;default:'user'"`
	HasAdminReply bool           `json:"has_admin_reply" gorm:"not null;default:false"`
	CreatedAt     time.Time      `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt     time.Time      `json:"updated_at" gorm:"autoUpdateTime;index;index:idx_nexus_ticket_user_updated,priority:2;index:idx_nexus_ticket_status_updated,priority:2"`
	ClosedAt      *time.Time     `json:"closed_at,omitempty"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`
}

func (Ticket) TableName() string {
	return "nexus_tickets"
}

type TicketMessage struct {
	Id         int       `json:"id" gorm:"primaryKey"`
	TicketId   int       `json:"ticket_id" gorm:"not null;index:idx_nexus_ticket_message_ticket_time,priority:1"`
	AuthorId   int       `json:"author_id" gorm:"not null;index"`
	AuthorRole string    `json:"author_role" gorm:"type:varchar(16);not null"`
	Content    string    `json:"-" gorm:"not null"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime;index:idx_nexus_ticket_message_ticket_time,priority:2"`
}

func (TicketMessage) TableName() string {
	return "nexus_ticket_messages"
}

type TicketMessageView struct {
	Id         int       `json:"id"`
	AuthorRole string    `json:"author_role"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

type TicketView struct {
	Id            int                 `json:"id"`
	UserId        int                 `json:"user_id"`
	Username      string              `json:"username,omitempty"`
	Type          string              `json:"type"`
	Status        string              `json:"status"`
	LastAuthor    string              `json:"last_author"`
	HasAdminReply bool                `json:"has_admin_reply"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	ClosedAt      *time.Time          `json:"closed_at,omitempty"`
	Messages      []TicketMessageView `json:"messages,omitempty" gorm:"-"`
}

func NormalizeTicketType(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	_, ok := validTicketTypes[value]
	return value, ok
}

func NormalizeTicketStatus(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	_, ok := validTicketStatuses[value]
	return value, ok
}

func validateTicketContent(content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return ErrTicketContentRequired
	}
	settings := ticket_setting.GetSettings()
	if len([]rune(content)) > settings.MaxContentLength {
		return ErrTicketContentTooLong
	}
	return nil
}

func ticketView(ticket *Ticket, username string, messages []TicketMessage) (*TicketView, error) {
	view := &TicketView{
		Id:            ticket.Id,
		UserId:        ticket.UserId,
		Username:      username,
		Type:          ticket.Type,
		Status:        ticket.Status,
		LastAuthor:    ticket.LastAuthor,
		HasAdminReply: ticket.HasAdminReply,
		CreatedAt:     ticket.CreatedAt,
		UpdatedAt:     ticket.UpdatedAt,
		ClosedAt:      ticket.ClosedAt,
		Messages:      make([]TicketMessageView, 0, len(messages)),
	}
	for _, message := range messages {
		content, err := common.DecryptTicketContent(message.Content, ticket.Id)
		if err != nil {
			return nil, err
		}
		view.Messages = append(view.Messages, TicketMessageView{
			Id:         message.Id,
			AuthorRole: message.AuthorRole,
			Content:    content,
			CreatedAt:  message.CreatedAt,
		})
	}
	return view, nil
}

func CreateTicket(userId int, ticketType string, content string) (*TicketView, error) {
	ticketType, ok := NormalizeTicketType(ticketType)
	if !ok {
		return nil, ErrTicketInvalidType
	}
	if err := validateTicketContent(content); err != nil {
		return nil, err
	}
	var ticket Ticket
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id").First(&user, "id = ?", userId).Error; err != nil {
			return err
		}
		var openTickets int64
		if err := tx.Model(&Ticket{}).
			Where("user_id = ? AND status <> ?", userId, TicketStatusClosed).
			Count(&openTickets).Error; err != nil {
			return err
		}
		if openTickets >= MaxOpenTicketsPerUser {
			return ErrTicketOpenLimit
		}
		ticket = Ticket{
			UserId:     userId,
			Type:       ticketType,
			Status:     TicketStatusPending,
			LastAuthor: TicketAuthorUser,
		}
		if err := tx.Create(&ticket).Error; err != nil {
			return err
		}
		return createTicketMessage(tx, &ticket, userId, TicketAuthorUser, content)
	})
	if err != nil {
		return nil, err
	}
	return GetTicketView(ticket.Id, userId, false)
}

func createTicketMessage(tx *gorm.DB, ticket *Ticket, authorId int, authorRole string, content string) error {
	encrypted, err := common.EncryptTicketContent(strings.TrimSpace(content), ticket.Id)
	if err != nil {
		return err
	}
	message := TicketMessage{
		TicketId:   ticket.Id,
		AuthorId:   authorId,
		AuthorRole: authorRole,
		Content:    encrypted,
	}
	if err := tx.Create(&message).Error; err != nil {
		return err
	}
	updates := map[string]interface{}{
		"last_author": authorRole,
		"updated_at":  time.Now(),
	}
	if authorRole == TicketAuthorAdmin {
		updates["status"] = TicketStatusReplied
		updates["has_admin_reply"] = true
	} else {
		updates["status"] = TicketStatusPending
	}
	return tx.Model(ticket).Updates(updates).Error
}

func GetTicketView(ticketId int, userId int, admin bool) (*TicketView, error) {
	var ticket Ticket
	query := DB.Where("id = ?", ticketId)
	if !admin {
		query = query.Where("user_id = ?", userId)
	}
	if err := query.First(&ticket).Error; err != nil {
		return nil, err
	}
	var messages []TicketMessage
	if err := DB.Where("ticket_id = ?", ticketId).Order("created_at ASC, id ASC").Find(&messages).Error; err != nil {
		return nil, err
	}
	username := ""
	if admin {
		var user User
		if err := DB.Select("username").First(&user, "id = ?", ticket.UserId).Error; err == nil {
			username = user.Username
		}
	}
	return ticketView(&ticket, username, messages)
}

func listTicketSummaries(query *gorm.DB, pageInfo *common.PageInfo, includeUsername bool) ([]TicketView, int64, error) {
	if pageInfo == nil {
		pageInfo = &common.PageInfo{Page: 1, PageSize: common.ItemsPerPage}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	fields := "nexus_tickets.id, nexus_tickets.user_id, nexus_tickets.type, nexus_tickets.status, nexus_tickets.last_author, nexus_tickets.has_admin_reply, nexus_tickets.created_at, nexus_tickets.updated_at, nexus_tickets.closed_at"
	if includeUsername {
		fields += ", users.username"
		query = query.Joins("LEFT JOIN users ON users.id = nexus_tickets.user_id")
	}
	views := make([]TicketView, 0, pageInfo.GetPageSize())
	err := query.Select(fields).
		Order("nexus_tickets.updated_at DESC, nexus_tickets.id DESC").
		Limit(pageInfo.GetPageSize()).
		Offset(pageInfo.GetStartIdx()).
		Scan(&views).Error
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

func ListTicketsByUser(userId int, pageInfo *common.PageInfo) ([]TicketView, int64, error) {
	query := DB.Model(&Ticket{}).Where("nexus_tickets.user_id = ?", userId)
	return listTicketSummaries(query, pageInfo, false)
}

func ListTicketsForAdmin(status string, pageInfo *common.PageInfo) ([]TicketView, int64, error) {
	if status != "" {
		var ok bool
		status, ok = NormalizeTicketStatus(status)
		if !ok {
			return nil, 0, ErrTicketInvalidStatus
		}
	}
	query := DB.Model(&Ticket{})
	if status != "" {
		query = query.Where("nexus_tickets.status = ?", status)
	}
	return listTicketSummaries(query, pageInfo, true)
}

func AddTicketReply(ticketId int, authorId int, authorRole string, content string) (*TicketView, error) {
	if authorRole != TicketAuthorUser && authorRole != TicketAuthorAdmin {
		return nil, ErrTicketInvalidAuthor
	}
	if err := validateTicketContent(content); err != nil {
		return nil, err
	}
	var ticket Ticket
	err := DB.Transaction(func(tx *gorm.DB) error {
		query := lockForUpdate(tx).Where("id = ?", ticketId)
		if authorRole == TicketAuthorUser {
			query = query.Where("user_id = ?", authorId)
		}
		if err := query.First(&ticket).Error; err != nil {
			return err
		}
		if ticket.Status == TicketStatusClosed {
			return ErrTicketClosed
		}
		var messageCount int64
		if err := tx.Model(&TicketMessage{}).Where("ticket_id = ?", ticketId).Count(&messageCount).Error; err != nil {
			return err
		}
		if messageCount >= MaxMessagesPerTicket {
			return ErrTicketMessageLimit
		}
		return createTicketMessage(tx, &ticket, authorId, authorRole, content)
	})
	if err != nil {
		return nil, err
	}
	return GetTicketView(ticketId, authorId, authorRole == TicketAuthorAdmin)
}

func SetTicketStatus(ticketId int, status string) (*TicketView, error) {
	status, ok := NormalizeTicketStatus(status)
	if !ok {
		return nil, ErrTicketInvalidStatus
	}
	var ticket Ticket
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&ticket, "id = ?", ticketId).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{"status": status}
		if status == TicketStatusClosed {
			now := time.Now()
			updates["closed_at"] = &now
		} else {
			updates["closed_at"] = nil
		}
		return tx.Model(&ticket).Updates(updates).Error
	})
	if err != nil {
		return nil, err
	}
	return GetTicketView(ticketId, ticket.UserId, true)
}
