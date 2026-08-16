package error_mask_setting

import (
	"errors"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	MinErrorMaskRuleWeight     = 1
	MaxErrorMaskRuleWeight     = 10
	DefaultErrorMaskRuleWeight = 1
)

type ErrorMaskRule struct {
	Status      int    `json:"status"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Weight      int    `json:"weight"`
}

type errorMaskRuleJSON struct {
	Status      int    `json:"status"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Weight      *int   `json:"weight"`
}

type ErrorMaskSetting struct {
	Enabled bool            `json:"enabled"`
	Rules   []ErrorMaskRule `json:"rules"`
}

var errorMaskSetting = ErrorMaskSetting{
	Enabled: false,
	Rules:   nil,
}

func init() {
	config.GlobalConfig.Register("error_mask_setting", &errorMaskSetting)
}

func GetSetting() ErrorMaskSetting {
	rules := errorMaskSetting.Rules
	out := make([]ErrorMaskRule, len(rules))
	copy(out, rules)
	for i := range out {
		if out[i].Weight == 0 {
			out[i].Weight = DefaultErrorMaskRuleWeight
		}
	}
	// Stable sorting preserves the configured top-to-bottom order for rules
	// with the same weight while giving higher-weight rules priority.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Weight > out[j].Weight
	})
	return ErrorMaskSetting{
		Enabled: errorMaskSetting.Enabled,
		Rules:   out,
	}
}

func RulesJSONString() string {
	rules := GetSetting().Rules
	if len(rules) == 0 {
		return ""
	}
	data, err := common.Marshal(rules)
	if err != nil {
		return ""
	}
	return string(data)
}

func UpdateRulesByJSONString(jsonStr string) error {
	rules, err := ParseRulesJSONString(jsonStr)
	if err != nil {
		return err
	}
	errorMaskSetting.Rules = rules
	return nil
}

func ParseRulesJSONString(jsonStr string) ([]ErrorMaskRule, error) {
	if strings.TrimSpace(jsonStr) == "" {
		return nil, nil
	}
	var rawRules []errorMaskRuleJSON
	if err := common.UnmarshalJsonStr(jsonStr, &rawRules); err != nil {
		return nil, err
	}
	rules := make([]ErrorMaskRule, 0, len(rawRules))
	for _, rawRule := range rawRules {
		if rawRule.Status != 0 && (rawRule.Status < 100 || rawRule.Status > 599) {
			return nil, errors.New("error_mask rule status must be 0 or in [100,599]")
		}
		if strings.TrimSpace(rawRule.Replacement) == "" {
			return nil, errors.New("error_mask rule replacement must not be empty")
		}
		weight := DefaultErrorMaskRuleWeight
		if rawRule.Weight != nil {
			weight = *rawRule.Weight
			if weight < MinErrorMaskRuleWeight || weight > MaxErrorMaskRuleWeight {
				return nil, errors.New("error_mask rule weight must be in [1,10]")
			}
		}
		rules = append(rules, ErrorMaskRule{
			Status:      rawRule.Status,
			Pattern:     rawRule.Pattern,
			Replacement: rawRule.Replacement,
			Weight:      weight,
		})
	}
	return rules, nil
}

func CheckRules(jsonStr string) error {
	_, err := ParseRulesJSONString(jsonStr)
	return err
}
