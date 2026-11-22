package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const modelPricingBarrierOptionKey = "_model_pricing_barrier"

var modelPricingMutationMutex sync.Mutex
var modelMetadataDeleteMutex sync.Mutex

var modelPricingOptionKeys = []string{
	"AudioCompletionRatio", "AudioRatio", "CacheRatio", "CompletionRatio",
	"CreateCacheRatio", "ImageRatio", "ModelPrice", "ModelRatio",
	"billing_setting.billing_expr", "billing_setting.billing_mode",
}

func isModelPricingOptionKey(key string) bool {
	for _, pricingKey := range modelPricingOptionKeys {
		if key == pricingKey {
			return true
		}
	}
	return false
}

// 普通价格保存与联动删除共用写屏障；SQLite 也必须先取得写锁。
func lockModelPricingMutation(tx *gorm.DB) error {
	barrier := Option{Key: modelPricingBarrierOptionKey, Value: "1"}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "key"}}, DoNothing: true,
	}).Create(&barrier).Error; err != nil {
		return err
	}
	return tx.Model(&Option{}).Where(&Option{Key: modelPricingBarrierOptionKey}).
		UpdateColumn("value", gorm.Expr("value")).Error
}

type ModelDeleteResult struct {
	DeletedCount    int `json:"deleted_count"`
	UpdatedChannels int `json:"updated_channels"`
}

// DeleteModelMetadata 原子删除元数据及用户明确选择的精确渠道绑定、价格项。
// 任意记录缺失、规则模型联动或配置损坏都会回滚整批操作。
func DeleteModelMetadata(ids []int, removeFromChannels, removePricing bool) (ModelDeleteResult, error) {
	result := ModelDeleteResult{}
	if len(ids) == 0 || len(ids) > 1000 {
		return result, errors.New("请选择 1 至 1000 个模型")
	}
	seen := make(map[int]struct{}, len(ids))
	uniqueIDs := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return result, errors.New("模型 ID 必须为正整数")
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			uniqueIDs = append(uniqueIDs, id)
		}
	}
	sort.Ints(uniqueIDs)
	if removePricing {
		modelPricingMutationMutex.Lock()
		defer modelPricingMutationMutex.Unlock()
	}
	modelMetadataDeleteMutex.Lock()
	defer modelMetadataDeleteMutex.Unlock()

	published := make(map[string]string)
	err := DB.Transaction(func(tx *gorm.DB) error {
		if removePricing {
			if err := lockModelPricingMutation(tx); err != nil {
				return err
			}
		}
		var models []Model
		if err := lockForUpdate(tx).Where("id IN ?", uniqueIDs).Order("id").Find(&models).Error; err != nil {
			return err
		}
		if len(models) != len(uniqueIDs) {
			return errors.New("部分模型已不存在，请刷新后重试")
		}
		names := make(map[string]struct{}, len(models))
		for _, item := range models {
			if (removeFromChannels || removePricing) && item.NameRule != NameRuleExact {
				return errors.New("规则匹配模型不支持联动删除渠道或定价")
			}
			names[item.ModelName] = struct{}{}
		}
		if removeFromChannels {
			updated, err := removeExactModelsFromChannels(tx, names)
			if err != nil {
				return err
			}
			result.UpdatedChannels = updated
		}
		if removePricing {
			var err error
			published, err = removeExactModelPricing(tx, names)
			if err != nil {
				return err
			}
		}
		deleted := tx.Where("id IN ?", uniqueIDs).Delete(&Model{})
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != int64(len(uniqueIDs)) {
			return errors.New("模型状态已改变，请刷新后重试")
		}
		result.DeletedCount = len(uniqueIDs)
		return nil
	})
	if err != nil {
		return ModelDeleteResult{}, err
	}
	for _, key := range modelPricingOptionKeys {
		if value, ok := published[key]; ok {
			if err := updateOptionMap(key, value); err != nil {
				return result, fmt.Errorf("模型已删除，但价格缓存更新失败：%w", err)
			}
		}
	}
	if result.UpdatedChannels > 0 {
		InitChannelCache()
	}
	RefreshPricing()
	if removePricing {
		ratio_setting.InvalidateExposedDataCache()
	}
	return result, nil
}

func removeExactModelsFromChannels(tx *gorm.DB, names map[string]struct{}) (int, error) {
	var channels []Channel
	if err := lockForUpdate(tx).Select("id", "models", "group", "status", "priority", "weight", "tag").
		Order("id").Find(&channels).Error; err != nil {
		return 0, err
	}
	updated := 0
	for _, channel := range channels {
		models := channel.GetModels()
		kept := make([]string, 0, len(models))
		changed := false
		for _, name := range models {
			if _, remove := names[name]; remove {
				changed = true
				continue
			}
			kept = append(kept, name)
		}
		if !changed {
			continue
		}
		channel.Models = strings.Join(kept, ",")
		if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Update("models", channel.Models).Error; err != nil {
			return 0, err
		}
		if err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error; err != nil {
			return 0, err
		}
		if len(kept) > 0 {
			if err := channel.AddAbilities(tx); err != nil {
				return 0, err
			}
		}
		updated++
	}
	return updated, nil
}

func removeExactModelPricing(tx *gorm.DB, names map[string]struct{}) (map[string]string, error) {
	values := make(map[string]string, len(modelPricingOptionKeys))
	common.OptionMapRWMutex.RLock()
	for _, key := range modelPricingOptionKeys {
		if value, exists := common.OptionMap[key]; exists {
			values[key] = common.Interface2String(value)
		}
	}
	common.OptionMapRWMutex.RUnlock()
	var options []Option
	pricingKeys := make([]interface{}, len(modelPricingOptionKeys))
	for i, key := range modelPricingOptionKeys {
		pricingKeys[i] = key
	}
	if err := lockForUpdate(tx).Where(clause.IN{Column: clause.Column{Name: "key"}, Values: pricingKeys}).Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&options).Error; err != nil {
		return nil, err
	}
	for _, option := range options {
		values[option.Key] = option.Value
	}
	changed := make(map[string]string)
	for _, key := range modelPricingOptionKeys {
		value := strings.TrimSpace(values[key])
		if value == "" {
			continue
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal([]byte(value), &entries); err != nil || entries == nil {
			return nil, fmt.Errorf("模型价格配置 %s 无效，请先修复配置", key)
		}
		removed := false
		for name := range names {
			if _, exists := entries[name]; exists {
				delete(entries, name)
				removed = true
			}
		}
		if !removed {
			continue
		}
		encoded, err := json.Marshal(entries)
		if err != nil {
			return nil, err
		}
		// 提交前验证发布阶段所需的类型，避免数据库与运行时配置分离。
		if strings.HasPrefix(key, "billing_setting.") {
			var typed map[string]string
			err = json.Unmarshal(encoded, &typed)
		} else {
			var typed map[string]float64
			err = json.Unmarshal(encoded, &typed)
		}
		if err != nil {
			return nil, fmt.Errorf("模型价格配置 %s 类型无效：%w", key, err)
		}
		option := Option{Key: key, Value: string(encoded)}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&option).Error; err != nil {
			return nil, err
		}
		changed[key] = option.Value
	}
	return changed, nil
}
