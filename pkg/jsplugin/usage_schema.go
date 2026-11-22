package jsplugin

import (
	"errors"
	"strings"
)

func validateUsageField(field UsageFieldSchema) error {
	if len(field.Enum) != 0 {
		if field.Type != "" || field.Unit != "" || len(field.Enum) > 64 {
			return errors.New("enum usage fields cannot declare type/unit and allow at most 64 options")
		}
		seen := map[string]bool{}
		for _, option := range field.Enum {
			if option == "" || len(option) > 128 || strings.ContainsAny(option, "\r\n\x00") || seen[option] {
				return errors.New("invalid or duplicate usage enum option")
			}
			seen[option] = true
		}
		for option := range field.EnumLabels {
			if !seen[option] {
				return errors.New("usage enum label has no matching option")
			}
		}
		return nil
	}
	if field.Type == "boolean" {
		if field.Unit != "" || len(field.EnumLabels) != 0 {
			return errors.New("boolean usage fields cannot declare unit or enum labels")
		}
		return nil
	}
	if field.Type != "number" && field.Type != "" || len(field.EnumLabels) != 0 {
		return errors.New("unsupported usage field type")
	}
	return nil
}
