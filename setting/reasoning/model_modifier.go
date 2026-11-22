package reasoning

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ModelModifiers describes only explicit trailing controls. Legacy provider
// suffixes are still handled by their existing adapters.
type ModelModifiers struct {
	Base         string
	HasModifiers bool
	HasThinking  bool
	Mode         string
	Effort       string
	Budget       *int
	Temperature  *float64
	TopP         *float64
}

func ParseModelModifiers(modelName string) (ModelModifiers, error) {
	result := ModelModifiers{Base: modelName}
	parts := strings.Split(modelName, "@")
	first := len(parts)
	for i := len(parts) - 1; i > 0; i-- {
		key, _, ok := strings.Cut(parts[i], ":")
		if !ok || !modifierKey(key) {
			break
		}
		first = i
	}
	if first == len(parts) || strings.Join(parts[:first], "@") == "" {
		return result, nil
	}
	result.Base = strings.Join(parts[:first], "@")
	result.HasModifiers = true
	last := make(map[string]int)
	for i := first; i < len(parts); i++ {
		key, _, _ := strings.Cut(parts[i], ":")
		last[strings.ToLower(key)] = i
	}
	for i := first; i < len(parts); i++ {
		key, raw, _ := strings.Cut(parts[i], ":")
		key = strings.ToLower(key)
		if last[key] != i {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(raw))
		switch key {
		case "thinking":
			result.HasThinking = true
			switch value {
			case "off", "0":
				result.Mode, result.Effort, result.Budget = "disabled", "none", nil
			case "on", "adaptive":
				result.Mode = "enabled"
				if value == "adaptive" {
					result.Mode = "adaptive"
				}
				result.Budget = nil
				if result.Effort == "none" {
					result.Effort = ""
				}
			default:
				budget, err := strconv.Atoi(value)
				if err != nil || budget < -1 {
					return result, fmt.Errorf("invalid @thinking value %q", raw)
				}
				result.Mode, result.Budget = "enabled", &budget
				if budget == 0 {
					result.Mode, result.Effort, result.Budget = "disabled", "none", nil
				}
				if result.Mode != "disabled" && result.Effort == "none" {
					result.Effort = ""
				}
			}
		case "effort":
			switch value {
			case "none", "minimal", "low", "medium", "high", "xhigh", "max":
			default:
				return result, fmt.Errorf("invalid @effort value %q", raw)
			}
			result.HasThinking, result.Effort = true, value
			if value == "none" {
				result.Mode, result.Budget = "disabled", nil
			} else {
				if result.Mode == "" || result.Mode == "disabled" {
					result.Mode = "enabled"
				}
			}
		case "temperature", "topp":
			number, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return result, fmt.Errorf("invalid @%s value %q: expected a finite number", key, raw)
			}
			if key == "temperature" {
				result.Temperature = &number
			} else {
				result.TopP = &number
			}
		default:
			return result, fmt.Errorf("unsupported model modifier @%s; exempt this model in the thinking suffix setting if it is a real model ID", key)
		}
	}
	return result, nil
}

func modifierKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !letter && (i == 0 || r < '0' || r > '9') && (i == 0 || r != '_' && r != '-') {
			return false
		}
	}
	return true
}

// The mapped name supplies provider overrides; controls absent there retain
// their values from the client-facing name.
func MergeModelModifiers(origin, mapped ModelModifiers) ModelModifiers {
	result := origin
	result.Base = mapped.Base
	result.HasModifiers = origin.HasModifiers || mapped.HasModifiers
	if mapped.Temperature != nil {
		result.Temperature = mapped.Temperature
	}
	if mapped.TopP != nil {
		result.TopP = mapped.TopP
	}
	if mapped.HasThinking {
		result.HasThinking = true
		result.Mode = mapped.Mode
		if mapped.Mode == "disabled" {
			result.Effort, result.Budget = "none", nil
		} else {
			result.Effort, result.Budget = mapped.Effort, mapped.Budget
		}
	}
	return result
}
