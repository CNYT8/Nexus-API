package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Separate tables preserve every immutable revision and the compatibility-v1 receipts.
type ScriptPluginActivation struct {
	PluginKey string `json:"plugin_key" gorm:"primaryKey;type:varchar(30)"`
	PluginID  int64  `json:"plugin_id" gorm:"not null"`
	UpdatedAt int64  `json:"updated_at"`
}

func (ScriptPluginActivation) TableName() string { return "nexus_script_plugin_activations" }

type ScriptPluginTombstone struct {
	PluginID  int64 `json:"plugin_id" gorm:"primaryKey;autoIncrement:false"`
	DeletedAt int64 `json:"deleted_at"`
}

func (ScriptPluginTombstone) TableName() string { return "nexus_script_plugin_tombstones" }

type ScriptPluginMarketSource struct {
	ID      string `json:"id" gorm:"primaryKey;type:varchar(64)"`
	Name    string `json:"name" gorm:"type:varchar(128)"`
	URL     string `json:"url" gorm:"type:text"`
	Enabled bool   `json:"enabled"`
}

func (ScriptPluginMarketSource) TableName() string { return "nexus_script_plugin_market_sources" }

// A revision write lock serializes enable/bind/delete/activate across instances.
// The harmless UPDATE also obtains SQLite's writer lock before the checks.
func lockScriptRevision(tx *gorm.DB, id int64) (*ScriptPlugin, error) {
	if id <= 0 {
		return nil, errors.New("invalid script revision")
	}
	if err := tx.Model(&ScriptPlugin{}).Where("id = ?", id).UpdateColumn("enabled", gorm.Expr("enabled")).Error; err != nil {
		return nil, err
	}
	var revision ScriptPlugin
	if err := tx.First(&revision, id).Error; err != nil {
		return nil, err
	}
	var deleted int64
	if err := tx.Model(&ScriptPluginTombstone{}).Where("plugin_id = ?", id).Count(&deleted).Error; err != nil {
		return nil, err
	}
	if deleted != 0 {
		return nil, errors.New("script revision has been deleted")
	}
	return &revision, nil
}

func setScriptRevisionEnabled(id int64, enabled bool) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if _, err := lockScriptRevision(tx, id); err != nil {
			return err
		}
		return tx.Model(&ScriptPlugin{}).Where("id = ?", id).Update("enabled", enabled).Error
	})
}

func putScriptRevisionBinding(binding *ScriptPluginBinding) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		revision, err := lockScriptRevision(tx, binding.PluginID)
		if err != nil {
			return err
		}
		if !revision.Enabled {
			return errors.New("script revision is disabled")
		}
		binding.ID, binding.UpdatedAt = 0, time.Now().Unix()
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "channel_id"}, {Name: "model"}},
			DoUpdates: clause.AssignmentColumns([]string{"plugin_id", "updated_at"}),
		}).Create(binding).Error
	})
}

func ListScriptPluginActivations() ([]ScriptPluginActivation, error) {
	result := []ScriptPluginActivation{}
	err := DB.Order("plugin_key").Find(&result).Error
	return result, err
}
func GetScriptPluginActivation(key string) (*ScriptPluginActivation, error) {
	var result ScriptPluginActivation
	err := DB.Where("plugin_key = ?", key).First(&result).Error
	return &result, err
}
func ActivateScriptPluginRevision(id, expected int64) (*ScriptPluginActivation, error) {
	if expected < 0 {
		return nil, errors.New("invalid expected_plugin_id")
	}
	var result ScriptPluginActivation
	err := DB.Transaction(func(tx *gorm.DB) error {
		revision, err := lockScriptRevision(tx, id)
		if err != nil {
			return err
		}
		if !revision.Enabled {
			return errors.New("script revision is disabled")
		}
		result = ScriptPluginActivation{PluginKey: revision.Key, PluginID: id, UpdatedAt: time.Now().Unix()}
		if expected == 0 {
			created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&result)
			if created.Error != nil {
				return created.Error
			}
			if created.RowsAffected != 1 {
				return errors.New("active script revision changed; refresh and retry")
			}
			return nil
		}
		updated := tx.Model(&ScriptPluginActivation{}).Where("plugin_key = ? AND plugin_id = ?", revision.Key, expected).Updates(map[string]any{"plugin_id": id, "updated_at": result.UpdatedAt})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("active script revision changed; refresh and retry")
		}
		return nil
	})
	return &result, err
}
func DeleteScriptPluginRevision(id int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		revision, err := lockScriptRevision(tx, id)
		if err != nil {
			return err
		}
		if revision.Enabled {
			return errors.New("disable the script revision before deleting it")
		}
		for _, entity := range []any{&ScriptPluginBinding{}, &ScriptPluginActivation{}} {
			var count int64
			if err := tx.Model(entity).Where("plugin_id = ?", id).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return errors.New("script revision is bound or active")
			}
		}
		return tx.Create(&ScriptPluginTombstone{PluginID: id, DeletedAt: time.Now().Unix()}).Error
	})
}
func ListScriptPluginMarketSources() ([]ScriptPluginMarketSource, error) {
	result := []ScriptPluginMarketSource{}
	err := DB.Order("id").Find(&result).Error
	return result, err
}
func GetScriptPluginMarketSource(id string) (*ScriptPluginMarketSource, error) {
	var result ScriptPluginMarketSource
	err := DB.Where("id = ? AND enabled = ?", id, true).First(&result).Error
	return &result, err
}
func ReplaceScriptPluginMarketSources(sources []ScriptPluginMarketSource) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		ids := make([]string, 0, len(sources))
		for i := range sources {
			ids = append(ids, sources[i].ID)
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"name", "url", "enabled"})}).Create(&sources[i]).Error; err != nil {
				return err
			}
		}
		query := tx.Where("1 = 1")
		if len(ids) > 0 {
			query = tx.Where("id NOT IN ?", ids)
		}
		return query.Delete(&ScriptPluginMarketSource{}).Error
	})
}
