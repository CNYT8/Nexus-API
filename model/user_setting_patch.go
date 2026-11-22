package model

import (
	"encoding/json"

	"gorm.io/gorm"
)

// PatchUserSetting uses a write lock before reading so a notification update
// cannot erase a concurrently changed permission, language or billing setting.
func PatchUserSetting(userID int, patch map[string]any) error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&User{}).Where("id = ?", userID).UpdateColumn("auth_version", gorm.Expr("auth_version"))
		if result.Error != nil {
			return result.Error
		}
		// A no-op lock may report zero affected rows on MySQL. The SELECT below
		// determines whether the user exists.
		var user User
		if err := tx.Select("id", "setting").First(&user, userID).Error; err != nil {
			return err
		}
		values := map[string]json.RawMessage{}
		if user.Setting != "" {
			if err := json.Unmarshal([]byte(user.Setting), &values); err != nil {
				return err
			}
		}
		if values == nil {
			values = map[string]json.RawMessage{}
		}
		for key, value := range patch {
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			values[key] = encoded
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return err
		}
		updates := map[string]any{"setting": string(encoded)}
		if adminPermissionsChanged(user.Setting, string(encoded)) {
			updates["auth_version"] = gorm.Expr("auth_version + 1")
			updates["access_token"] = nil
		}
		return tx.Model(&User{}).Where("id = ?", userID).Updates(updates).Error
	})
	if err != nil {
		return err
	}
	return invalidateUserCache(userID)
}
