package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const activeLoginEncryptionKeySlot = "active"

// LoginEncryptionKey is internal key material, separate from public options.
// The unique slot makes concurrent initializers converge on one persisted key.
type LoginEncryptionKey struct {
	ID            uint   `json:"-" gorm:"primaryKey"`
	Slot          string `json:"-" gorm:"type:varchar(32);not null;uniqueIndex"`
	PrivateKeyPEM string `json:"-" gorm:"type:text;not null"`
}

// InitPasswordEncryption runs on every node after the master migrates the shared
// schema. A slave starting before migration fails closed and may be restarted.
func InitPasswordEncryption() error {
	if !common.PasswordLoginEncryptionEnabled {
		return nil
	}
	if DB == nil {
		return errors.New("password encryption database is unavailable")
	}
	var stored LoginEncryptionKey
	queryErr := DB.Where("slot = ?", activeLoginEncryptionKeySlot).First(&stored).Error
	if queryErr != nil && !errors.Is(queryErr, gorm.ErrRecordNotFound) {
		return fmt.Errorf("read password encryption key: %w", queryErr)
	}
	if errors.Is(queryErr, gorm.ErrRecordNotFound) {
		privateKeyPEM, err := common.GeneratePasswordEncryptionPrivateKey()
		if err != nil {
			return err
		}
		candidate := LoginEncryptionKey{Slot: activeLoginEncryptionKeySlot, PrivateKeyPEM: privateKeyPEM}
		if err := DB.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "slot"}}, DoNothing: true,
		}).Create(&candidate).Error; err != nil {
			return fmt.Errorf("persist password encryption key: %w", err)
		}
		// Never install the candidate: another replica may have won the insert.
		if err := DB.Where("slot = ?", activeLoginEncryptionKeySlot).First(&stored).Error; err != nil {
			return fmt.Errorf("reload password encryption key: %w", err)
		}
	}
	if err := common.LoadPasswordEncryptionPrivateKey(stored.PrivateKeyPEM); err != nil {
		return fmt.Errorf("load persisted password encryption key: %w", err)
	}
	return nil
}
