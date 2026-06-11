package error_mask_setting

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type ErrorMaskRule struct {
	Status      int    `json:"status"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
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
	return ErrorMaskSetting{
		Enabled: errorMaskSetting.Enabled,
		Rules:   out,
	}
}

func RulesJSONString() string {
	if len(errorMaskSetting.Rules) == 0 {
		return ""
	}
	data, err := common.Marshal(errorMaskSetting.Rules)
	if err != nil {
		return ""
	}
	return string(data)
}

func UpdateRulesByJSONString(jsonStr string) error {
	if strings.TrimSpace(jsonStr) == "" {
		errorMaskSetting.Rules = nil
		return nil
	}
	var rules []ErrorMaskRule
	if err := common.UnmarshalJsonStr(jsonStr, &rules); err != nil {
		return err
	}
	errorMaskSetting.Rules = rules
	return nil
}

func CheckRules(jsonStr string) error {
	if strings.TrimSpace(jsonStr) == "" {
		return nil
	}
	var rules []ErrorMaskRule
	if err := common.UnmarshalJsonStr(jsonStr, &rules); err != nil {
		return err
	}
	for _, r := range rules {
		if r.Status != 0 && (r.Status < 100 || r.Status > 599) {
			return errors.New("error_mask rule status must be 0 or in [100,599]")
		}
		if strings.TrimSpace(r.Replacement) == "" {
			return errors.New("error_mask rule replacement must not be empty")
		}
	}
	return nil
}
