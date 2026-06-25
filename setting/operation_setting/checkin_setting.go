package operation_setting

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type CheckinStageRule struct {
	RequestThreshold int  `json:"request_threshold"` // 前一天调用次数需超过，0 表示不限制
	TokenThreshold   int  `json:"token_threshold"`   // 前一天 token 用量需超过，0 表示不限制
	AllowCheckin     bool `json:"allow_checkin"`     // 是否允许该阶段签到
	MinQuota         int  `json:"min_quota"`         // 该阶段最小额度奖励
	MaxQuota         int  `json:"max_quota"`         // 该阶段最大额度奖励
}

// CheckinSetting 签到功能配置
type CheckinSetting struct {
	Enabled          bool               `json:"enabled"`           // 是否启用签到功能
	MinQuota         int                `json:"min_quota"`         // 签到最小额度奖励
	MaxQuota         int                `json:"max_quota"`         // 签到最大额度奖励
	ConditionEnabled bool               `json:"condition_enabled"` // 是否启用阶段签到（兼容旧配置键）
	RequestThreshold int                `json:"request_threshold"` // 前一天调用次数需超过
	TokenThreshold   int                `json:"token_threshold"`   // 前一天 token 用量需超过
	StageRules       []CheckinStageRule `json:"stage_rules"`       // 阶段签到规则
}

// 默认配置
var checkinSetting = CheckinSetting{
	Enabled:          false, // 默认关闭
	MinQuota:         1000,  // 默认最小额度 1000 (约 0.002 USD)
	MaxQuota:         10000, // 默认最大额度 10000 (约 0.02 USD)
	ConditionEnabled: false, // 默认不限制前一天用量
	RequestThreshold: 0,
	TokenThreshold:   0,
	StageRules:       nil,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("checkin_setting", &checkinSetting)
}

// GetCheckinSetting 获取签到配置
func GetCheckinSetting() *CheckinSetting {
	return &checkinSetting
}

// IsCheckinEnabled 是否启用签到功能
func IsCheckinEnabled() bool {
	return checkinSetting.Enabled
}

// GetCheckinQuotaRange 获取签到额度范围
func GetCheckinQuotaRange() (min, max int) {
	return checkinSetting.MinQuota, checkinSetting.MaxQuota
}

func CheckinStageRulesJSONString() string {
	if len(checkinSetting.StageRules) == 0 {
		return ""
	}
	data, err := common.Marshal(checkinSetting.StageRules)
	if err != nil {
		return ""
	}
	return string(data)
}

func UpdateCheckinStageRulesByJSONString(jsonStr string) error {
	rules, err := ParseCheckinStageRules(jsonStr)
	if err != nil {
		return err
	}
	checkinSetting.StageRules = rules
	return nil
}

func ParseCheckinStageRules(jsonStr string) ([]CheckinStageRule, error) {
	if strings.TrimSpace(jsonStr) == "" {
		return nil, nil
	}
	var rules []CheckinStageRule
	if err := common.UnmarshalJsonStr(jsonStr, &rules); err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].RequestThreshold < 0 || rules[i].TokenThreshold < 0 ||
			rules[i].MinQuota < 0 || rules[i].MaxQuota < 0 {
			return nil, errors.New("checkin stage values must not be negative")
		}
		if rules[i].AllowCheckin && rules[i].MaxQuota < rules[i].MinQuota {
			return nil, errors.New("checkin stage max_quota must be greater than or equal to min_quota")
		}
	}
	return rules, nil
}
