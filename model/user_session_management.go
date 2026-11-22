package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const UserSessionListLimit = 100

var ErrManagedSessionNotActive = errors.New("session not found or no longer active")

// Explicit public projection: never return the cookie secret, hash, security
// version or recent-verification proof to a client.
type ManagedUserSession struct {
	Id          int64  `json:"id"`
	Current     bool   `json:"current"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
	RevokedAt   int64  `json:"revoked_at"`
	LoginMethod string `json:"login_method"`
	IP          string `json:"ip"`
	UserAgent   string `json:"user_agent"`
}

type ManagedUserSessionList struct {
	Items   []ManagedUserSession `json:"items"`
	Limit   int                  `json:"limit"`
	HasMore bool                 `json:"has_more"`
}

func managedSessionIdentity(db *gorm.DB, userID int, currentID int64) (*User, *UserSession, error) {
	if userID <= 0 || currentID <= 0 {
		return nil, nil, ErrAuthStateInvalid
	}
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		return nil, nil, err
	}
	if user.Status != common.UserStatusEnabled {
		return nil, nil, ErrAuthStateInvalid
	}
	var current UserSession
	if err := db.Where("id = ? AND user_id = ? AND auth_version = ? AND revoked_at = 0 AND expires_at > ?",
		currentID, userID, user.AuthVersion, time.Now().Unix()).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrAuthStateInvalid
		}
		return nil, nil, err
	}
	return &user, &current, nil
}

func ListUserSessions(userID int, currentID int64) (*ManagedUserSessionList, error) {
	user, current, err := managedSessionIdentity(DB, userID, currentID)
	if err != nil {
		return nil, err
	}
	var others []UserSession
	// Fetch one extra row to distinguish a complete list from the bounded view.
	if err := DB.Where("user_id = ? AND id <> ?", userID, currentID).
		Order("id DESC").Limit(UserSessionListLimit).Find(&others).Error; err != nil {
		return nil, err
	}
	result := &ManagedUserSessionList{Items: make([]ManagedUserSession, 0, UserSessionListLimit), Limit: UserSessionListLimit,
		HasMore: len(others) >= UserSessionListLimit}
	if result.HasMore {
		others = others[:UserSessionListLimit-1]
	}
	records := append([]UserSession{*current}, others...)
	now := time.Now().Unix()
	for _, record := range records {
		status := "active"
		switch {
		case record.RevokedAt != 0:
			status = "revoked"
		case record.AuthVersion != user.AuthVersion:
			status = "invalidated"
		case record.ExpiresAt <= now:
			status = "expired"
		}
		result.Items = append(result.Items, ManagedUserSession{Id: record.Id, Current: record.Id == currentID, Status: status,
			CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt, RevokedAt: record.RevokedAt,
			LoginMethod: record.LoginMethod, IP: record.IP, UserAgent: record.UserAgent})
	}
	return result, nil
}

func withManagedSession(userID int, currentID int64, action func(*gorm.DB, *User) error) error {
	if userID <= 0 || currentID <= 0 {
		return ErrAuthStateInvalid
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		// Same per-user write lock as session creation/security-version changes.
		// Re-read after locking; MySQL may report zero rows for this no-op update.
		if err := tx.Model(&User{}).Where("id = ?", userID).
			UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
			return err
		}
		user, _, err := managedSessionIdentity(tx, userID, currentID)
		if err != nil {
			return err
		}
		return action(tx, user)
	})
}

func RevokeUserSessionByID(userID int, currentID, targetID int64) (bool, error) {
	if targetID <= 0 {
		return false, ErrManagedSessionNotActive
	}
	err := withManagedSession(userID, currentID, func(tx *gorm.DB, user *User) error {
		now := time.Now().Unix()
		result := tx.Model(&UserSession{}).
			Where("id = ? AND user_id = ? AND auth_version = ? AND revoked_at = 0 AND expires_at > ?", targetID, userID, user.AuthVersion, now).
			Update("revoked_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrManagedSessionNotActive
		}
		return nil
	})
	return err == nil && targetID == currentID, err
}

func RevokeOtherUserSessions(userID int, currentID int64) (int64, error) {
	var count int64
	err := withManagedSession(userID, currentID, func(tx *gorm.DB, user *User) error {
		now := time.Now().Unix()
		result := tx.Model(&UserSession{}).
			Where("user_id = ? AND id <> ? AND auth_version = ? AND revoked_at = 0 AND expires_at > ?", userID, currentID, user.AuthVersion, now).
			Update("revoked_at", now)
		count = result.RowsAffected
		return result.Error
	})
	return count, err
}
