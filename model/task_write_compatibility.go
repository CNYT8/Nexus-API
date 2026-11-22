package model

import (
	"bytes"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Lock every owner in a stable order before locking tasks. The discovery read
// is outside the transaction so SQLite does not upgrade an old read snapshot.
// Only the locked row decides whether a write is allowed.
func guardedTaskWrites(query *gorm.DB, skipProtected bool, write func(*gorm.DB, *Task) error) error {
	var owners []Task
	if err := query.Select("id", "user_id").Order("user_id").Order("id").Find(&owners).Error; err != nil {
		return err
	}
	if len(owners) == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		lastUserID := 0
		for i, owner := range owners {
			if i == 0 || owner.UserId != lastUserID {
				if err := LockTaskBillingUserTx(tx, owner.UserId); err != nil {
					return err
				}
				lastUserID = owner.UserId
			}
		}
		for _, owner := range owners {
			var stored Task
			// Reapply the selector so an upstream ID changed since discovery
			// cannot make this batch update a row that no longer matches.
			if err := walletCurrentTx(tx).Where(query.Statement.Clauses["WHERE"].Expression).First(&stored, owner.ID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
			if stored.HasForeignExecutionEvidence() {
				if skipProtected {
					continue
				}
				return ErrTaskExecutionProtected
			}
			if stored.UserId != owner.UserId {
				return errors.New("task owner changed while acquiring write locks")
			}
			if err := write(tx, &stored); err != nil {
				return err
			}
		}
		return nil
	})
}

// Generic writes are patches to private_data, never an implicit clear. Read
// known struct fields only: a stale caller must not restore old unknown fields.
func mergeTaskWritePrivateData(stored TaskPrivateData, incoming any) (TaskPrivateData, error) {
	var patch []byte
	var err error
	switch value := incoming.(type) {
	case nil:
		return stored, nil
	case TaskPrivateData:
		patch, err = json.Marshal(taskPrivateDataFields(value))
	case *TaskPrivateData:
		if value == nil {
			return stored, nil
		}
		patch, err = json.Marshal(taskPrivateDataFields(*value))
	case string:
		patch = []byte(value)
	case []byte:
		patch = value
	case json.RawMessage:
		patch = value
	case clause.Expression:
		return TaskPrivateData{}, errors.New("task private_data requires a JSON object")
	default:
		patch, err = json.Marshal(value)
	}
	if err != nil {
		return TaskPrivateData{}, err
	}
	if len(bytes.TrimSpace(patch)) == 0 || bytes.Equal(bytes.TrimSpace(patch), []byte("null")) {
		return stored, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(patch, &fields); err != nil {
		return TaskPrivateData{}, err
	}
	original, err := stored.MarshalJSON()
	if err != nil {
		return TaskPrivateData{}, err
	}
	merged, err := mergeTaskWriteJSON(original, patch)
	if err != nil {
		return TaskPrivateData{}, err
	}
	var result TaskPrivateData
	if err := result.UnmarshalJSON(merged); err != nil {
		return TaskPrivateData{}, err
	}
	if (&Task{PrivateData: result}).HasForeignExecutionEvidence() {
		return TaskPrivateData{}, ErrTaskExecutionProtected
	}
	return result, nil
}

func mergeTaskWriteJSON(original, patch []byte) ([]byte, error) {
	if bytes.Equal(bytes.TrimSpace(patch), []byte("null")) {
		return original, nil
	}
	var storedFields, patchFields map[string]json.RawMessage
	if json.Unmarshal(original, &storedFields) == nil && storedFields != nil &&
		json.Unmarshal(patch, &patchFields) == nil && patchFields != nil {
		for key, value := range patchFields {
			if old, exists := storedFields[key]; exists {
				var err error
				value, err = mergeTaskWriteJSON(old, value)
				if err != nil {
					return nil, err
				}
			}
			storedFields[key] = value
		}
		return json.Marshal(storedFields)
	}
	var storedItems, patchItems []json.RawMessage
	if json.Unmarshal(original, &storedItems) == nil && json.Unmarshal(patch, &patchItems) == nil && len(storedItems) == len(patchItems) {
		for i := range storedItems {
			var err error
			patchItems[i], err = mergeTaskWriteJSON(storedItems[i], patchItems[i])
			if err != nil {
				return nil, err
			}
		}
		return json.Marshal(patchItems)
	}
	return patch, nil
}

func guardedTaskBulkUpdate(query *gorm.DB, params map[string]any) error {
	return guardedTaskWrites(query, true, func(tx *gorm.DB, stored *Task) error {
		updates := make(map[string]any, len(params))
		for key, value := range params {
			if key == "private_data" || key == "PrivateData" {
				merged, err := mergeTaskWritePrivateData(stored.PrivateData, value)
				if err != nil {
					return err
				}
				value = merged
			}
			updates[key] = value
		}
		return tx.Model(stored).Updates(updates).Error
	})
}
