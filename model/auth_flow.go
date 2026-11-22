package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// AuthFlow binds every ceremony to its browser, provider, purpose and account
// version. Payloads and challenges remain on the server, never in cookies.
type AuthFlow struct {
	Id          int64  `gorm:"primaryKey"`
	SecretHash  string `gorm:"type:char(64);uniqueIndex;not null"`
	Purpose     string `gorm:"type:varchar(64);not null"`
	Provider    string `gorm:"type:varchar(64)"`
	BindingHash string `gorm:"type:char(64);not null"`
	UserId      int    `gorm:"index"`
	AuthVersion int64
	Payload     string `gorm:"type:text"`
	ExpiresAt   int64  `gorm:"index"`
	ConsumedAt  int64
}

type AuthFlowMatch struct {
	Purpose  string
	Provider string
	Binding  string
	UserId   int
}

func CreateAuthFlow(match AuthFlowMatch, payload string, lifetime time.Duration) (string, error) {
	if match.Purpose == "" || match.Binding == "" || lifetime <= 0 || lifetime > 10*time.Minute {
		return "", ErrAuthStateInvalid
	}
	secret, err := NewAuthSecret()
	if err != nil {
		return "", err
	}
	flow := AuthFlow{SecretHash: authSecretHash("flow", secret), Purpose: match.Purpose, Provider: match.Provider,
		BindingHash: authSecretHash("binding", match.Binding), UserId: match.UserId, Payload: payload,
		ExpiresAt: time.Now().Add(lifetime).Unix()}
	if match.UserId > 0 {
		user, err := GetUserById(match.UserId, false)
		if err != nil {
			return "", err
		}
		flow.AuthVersion = user.AuthVersion
	}
	return secret, DB.Create(&flow).Error
}

func authFlowQuery(tx *gorm.DB, secret string, match AuthFlowMatch) *gorm.DB {
	return tx.Where("secret_hash = ? AND purpose = ? AND provider = ? AND binding_hash = ? AND user_id = ?",
		authSecretHash("flow", secret), match.Purpose, match.Provider, authSecretHash("binding", match.Binding), match.UserId)
}

func GetAuthFlow(secret string, match AuthFlowMatch) (*AuthFlow, error) {
	if secret == "" || match.Purpose == "" || match.Binding == "" {
		return nil, ErrAuthStateInvalid
	}
	var flow AuthFlow
	if err := authFlowQuery(DB, secret, match).Where("consumed_at = 0 AND expires_at > ?", time.Now().Unix()).First(&flow).Error; err != nil {
		return nil, err
	}
	if flow.UserId > 0 {
		user, err := GetUserById(flow.UserId, false)
		if err != nil || user.AuthVersion != flow.AuthVersion || user.Status != common.UserStatusEnabled {
			return nil, ErrAuthStateInvalid
		}
	}
	return &flow, nil
}

func ConsumeAuthFlow(secret string, match AuthFlowMatch) (*AuthFlow, error) {
	if secret == "" || match.Purpose == "" || match.Binding == "" {
		return nil, ErrAuthStateInvalid
	}
	var flow AuthFlow
	err := DB.Transaction(func(tx *gorm.DB) error {
		now := time.Now().Unix()
		result := authFlowQuery(tx.Model(&AuthFlow{}), secret, match).
			Where("consumed_at = 0 AND expires_at > ?", now).Update("consumed_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAuthStateInvalid
		}
		if err := authFlowQuery(tx, secret, match).First(&flow).Error; err != nil {
			return err
		}
		if flow.UserId > 0 {
			var user User
			if err := tx.Select("id", "auth_version", "status").First(&user, flow.UserId).Error; err != nil {
				return err
			}
			if user.AuthVersion != flow.AuthVersion || user.Status != common.UserStatusEnabled {
				return ErrAuthStateInvalid
			}
		}
		return nil
	})
	return &flow, err
}

// ClaimTelegramAssertion prevents a valid signed assertion being replayed in a
// different browser or purpose. Keep claims longer than the signature lifetime.
func ClaimTelegramAssertion(signature string) error {
	flow := AuthFlow{SecretHash: authSecretHash("telegram-assertion", signature), Purpose: "telegram_assertion",
		ExpiresAt: time.Now().Add(10 * time.Minute).Unix(), ConsumedAt: time.Now().Unix()}
	return DB.Create(&flow).Error
}

func DeleteExpiredAuthenticationState(now time.Time) error {
	return DeleteExpiredAuthenticationStateWithContext(context.Background(), now)
}

func DeleteExpiredAuthenticationStateWithContext(ctx context.Context, now time.Time) error {
	db := DB.WithContext(ctx)
	if err := db.Where("expires_at < ?", now.Add(-24*time.Hour).Unix()).Delete(&AuthFlow{}).Error; err != nil {
		return err
	}
	// Retain legacy-cookie tombstones beyond the cookie store's 30-day max age.
	if err := db.Where("expires_at < ?", now.Add(-UserSessionLifetime).Unix()).Delete(&UserSession{}).Error; err != nil {
		return err
	}
	return db.Where("expires_at < ?", now.Unix()).Delete(&AccountSecurityLimit{}).Error
}
