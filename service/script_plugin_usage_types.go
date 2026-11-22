package service

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

func taskUsageDefault(field model.TaskUsageField) (any, error) {
	if len(field.Enum) > 0 {
		if field.Type != "" && field.Type != "enum" || len(field.Enum) > 64 {
			return nil, errors.New("invalid usage enum")
		}
		seen := map[string]bool{}
		for _, option := range field.Enum {
			if option == "" || len(option) > 128 || strings.ContainsAny(option, "\r\n\x00") || seen[option] {
				return nil, errors.New("invalid usage enum option")
			}
			seen[option] = true
		}
		value := field.DefaultString
		if value == "" && field.Required {
			value = field.Enum[0]
		}
		return validateTaskUsageValue(field, value)
	}
	if field.Type == "boolean" {
		return field.DefaultBool, nil
	}
	if field.Type != "" && field.Type != "number" || !finiteNonnegative(field.Max) || field.Max <= 0 || field.Max > 1e12 {
		return nil, errors.New("invalid numeric usage schema")
	}
	return validateTaskUsageValue(field, field.Default)
}

func validateTaskUsageValue(field model.TaskUsageField, value any) (any, error) {
	if len(field.Enum) > 0 {
		text, ok := value.(string)
		if ok {
			for _, option := range field.Enum {
				if text == option {
					return text, nil
				}
			}
		}
		return nil, errors.New("usage value is not a declared enum option")
	}
	if field.Type == "boolean" {
		if value, ok := value.(bool); ok {
			return value, nil
		}
		return nil, errors.New("usage value is not boolean")
	}
	var number float64
	switch v := value.(type) {
	case json.Number:
		var err error
		number, err = v.Float64()
		if err != nil {
			return nil, err
		}
	case float64:
		number = v
	default:
		return nil, errors.New("usage value is not numeric")
	}
	if !finiteNonnegative(number) || number > field.Max {
		return nil, errors.New("usage value is out of range")
	}
	return number, nil
}
