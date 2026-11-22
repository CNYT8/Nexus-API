package jsplugin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
)

var usageName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

func validateUsageHooks(ctx context.Context, engine *Engine, meta Meta) error {
	schemas := []map[string]UsageFieldSchema{meta.UsageSchema}
	for _, profile := range meta.UsageProfiles {
		if len(profile.Models) == 0 {
			return errors.New("usage profile requires models")
		}
		schemas = append(schemas, profile.Schema)
	}
	declared := false
	for _, schema := range schemas {
		if len(schema) > 32 {
			return errors.New("at most 32 usage fields are supported")
		}
		for name, field := range schema {
			declared = true
			if !usageName.MatchString(name) {
				return errors.New("invalid usage field name")
			}
			if err := validateUsageField(field); err != nil {
				return err
			}
			switch field.Unit {
			case "", "second", "count", "token", "credit":
			default:
				return errors.New("unsupported usage unit")
			}
		}
	}
	hooks := false
	for _, name := range []string{"extractUsage", "extractUsageOnSubmit", "extractUsageOnComplete"} {
		if !engine.HasExport(name) {
			continue
		}
		hooks = true
		callable, err := engine.HasCallablePath(ctx, name)
		if err != nil || !callable {
			return fmt.Errorf("usage hook %q must be callable", name)
		}
	}
	if declared || hooks {
		if !declared || !engine.HasExport("extractUsage") {
			return errors.New("usage plugins require a usage schema and extractUsage estimate hook")
		}
	}
	return nil
}

// ValidateUsageFacts preserves zero and fractions. Invalid facts fail as one
// unit; no partial overlay can accidentally turn a bad result into a refund.
func ValidateUsageFacts(value any, schema map[string]UsageFieldSchema) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("usage hook must return an object")
	}
	facts := make(map[string]any, len(object))
	for name, value := range object {
		field, declared := schema[name]
		if !declared {
			return nil, fmt.Errorf("undeclared usage field %q", name)
		}
		if err := validateUsageField(field); err != nil {
			return nil, err
		}
		if len(field.Enum) != 0 {
			text, ok := value.(string)
			valid := false
			for _, option := range field.Enum {
				valid = valid || text == option
			}
			if !ok || !valid {
				return nil, fmt.Errorf("invalid enum usage field %q", name)
			}
			facts[name] = text
			continue
		}
		if field.Type == "boolean" {
			boolean, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("invalid boolean usage field %q", name)
			}
			facts[name] = boolean
			continue
		}
		var number float64
		switch v := value.(type) {
		case float64:
			number = v
		case int64:
			number = float64(v)
		case int:
			number = float64(v)
		default:
			return nil, errors.New("usage facts must be numeric")
		}
		limit := float64(3600)
		switch field.Unit {
		case "count":
			limit = 128
		case "token", "credit":
			limit = 2147483647
		}
		if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > limit {
			return nil, errors.New("usage fact exceeds the host limit")
		}
		facts[name] = number
	}
	return facts, nil
}
