package model

import (
	"encoding/json"
	"errors"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// ValidateTaskPluginBindingsJSON is also suitable for the generic option save
// hook. Validate before persistence, never from the config read/update lock.
func ValidateTaskPluginBindingsJSON(value string) error {
	if len(value) > 64*1024 {
		return errors.New("task usage bindings exceed size limit")
	}
	var bindings map[string]int64
	if err := json.Unmarshal([]byte(value), &bindings); err != nil {
		return err
	}
	if err := operation_setting.ValidateTaskUsagePluginBindings(bindings); err != nil {
		return err
	}
	ids := make([]int64, 0, len(bindings))
	seen := make(map[int64]bool)
	for _, id := range bindings {
		if id > 0 && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var count int64
	if err := DB.Model(&TaskPlugin{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return errors.New("task usage binding references an unknown plugin revision")
	}
	return nil
}
