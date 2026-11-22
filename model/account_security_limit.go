package model

import (
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Shared by all instances. Only a keyed digest of the account/action is stored.
type AccountSecurityLimit struct {
	BucketHash string `gorm:"type:char(64);primaryKey"`
	Attempts   int    `gorm:"not null"`
	ExpiresAt  int64  `gorm:"index"`
}

func TakeAccountSecurityLimit(userID int, action string, maximum int, window time.Duration) (bool, error) {
	if userID <= 0 || action == "" || maximum <= 0 || window < time.Second {
		return false, ErrAuthStateInvalid
	}
	seconds := int64(window / time.Second)
	now := time.Now().Unix()
	bucket := now / seconds
	record := AccountSecurityLimit{BucketHash: authSecretHash("security-limit", strconv.Itoa(userID)+":"+action+":"+strconv.FormatInt(bucket, 10)), ExpiresAt: (bucket + 1) * seconds}
	allowed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error; err != nil {
			return err
		}
		result := tx.Model(&AccountSecurityLimit{}).Where("bucket_hash = ? AND attempts < ?", record.BucketHash, maximum).
			UpdateColumn("attempts", gorm.Expr("attempts + 1"))
		allowed = result.RowsAffected == 1
		return result.Error
	})
	return allowed, err
}
