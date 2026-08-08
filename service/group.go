package service

import (
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

type UserGroupRatioDetail struct {
	Group              string                       `json:"group"`
	BaseRatio          float64                      `json:"base_ratio"`
	MembershipRatio    float64                      `json:"membership_ratio"`
	EffectiveRatio     float64                      `json:"effective_ratio"`
	CustomRatio        *float64                     `json:"custom_ratio"`
	HasCustomRatio     bool                         `json:"has_custom_ratio"`
	MembershipDiscount model.MembershipDiscountInfo `json:"membership_discount"`
}

func GetUserUsableGroups(userGroup string) map[string]string {
	groupsCopy := setting.GetUserUsableGroupsCopy()
	if userGroup != "" {
		specialSettings, b := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(userGroup)
		if b {
			// 处理特殊可用分组
			for specialGroup, desc := range specialSettings {
				if strings.HasPrefix(specialGroup, "-:") {
					// 移除分组
					groupToRemove := strings.TrimPrefix(specialGroup, "-:")
					delete(groupsCopy, groupToRemove)
				} else if strings.HasPrefix(specialGroup, "+:") {
					// 添加分组
					groupToAdd := strings.TrimPrefix(specialGroup, "+:")
					groupsCopy[groupToAdd] = desc
				} else {
					// 直接添加分组
					groupsCopy[specialGroup] = desc
				}
			}
		}
		// 如果userGroup不在UserUsableGroups中，返回UserUsableGroups + userGroup
		if _, ok := groupsCopy[userGroup]; !ok {
			groupsCopy[userGroup] = "用户分组"
		}
	}
	return groupsCopy
}

func GroupInUserUsableGroups(userGroup, groupName string) bool {
	_, ok := GetUserUsableGroups(userGroup)[groupName]
	return ok
}

// GetUserAutoGroup 根据用户分组获取自动分组设置
func GetUserAutoGroup(userGroup string) []string {
	groups := GetUserUsableGroups(userGroup)
	autoGroups := make([]string, 0)
	for _, group := range setting.GetAutoGroups() {
		if _, ok := groups[group]; ok {
			autoGroups = append(autoGroups, group)
		}
	}
	return autoGroups
}

// GetGroupsEnabledModels returns enabled models in group order without duplicates.
func GetGroupsEnabledModels(groups []string) []string {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, group := range groups {
		for _, modelName := range model.GetGroupEnabledModels(group) {
			if _, ok := seen[modelName]; ok {
				continue
			}
			seen[modelName] = struct{}{}
			models = append(models, modelName)
		}
	}
	return models
}

// GetUserGroupRatio 获取用户使用某个分组的倍率
// userGroup 用户分组
// group 需要获取倍率的分组
func GetUserGroupRatio(userGroup, group string) float64 {
	ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, group)
	if ok {
		return ratio
	}
	return ratio_setting.GetGroupRatio(group)
}

func GetUserGroupRatioDetails(userId int, userGroup string) ([]UserGroupRatioDetail, error) {
	overrides, err := model.ListUserGroupRatioOverrides(userId)
	if err != nil {
		return nil, err
	}
	groupRatios := ratio_setting.GetGroupRatioCopy()
	groupNames := make([]string, 0, len(groupRatios))
	for group := range groupRatios {
		groupNames = append(groupNames, group)
	}
	sort.Strings(groupNames)

	details := make([]UserGroupRatioDetail, 0, len(groupNames))
	for _, group := range groupNames {
		baseRatio := GetUserGroupRatio(userGroup, group)
		membershipRatio, membershipDiscount := model.ApplyMembershipDiscount(userId, group, baseRatio)
		effectiveRatio := membershipRatio
		customRatio, hasCustomRatio := overrides[group]
		var customRatioPointer *float64
		if hasCustomRatio {
			effectiveRatio = customRatio
			customRatioCopy := customRatio
			customRatioPointer = &customRatioCopy
		}
		details = append(details, UserGroupRatioDetail{
			Group:              group,
			BaseRatio:          baseRatio,
			MembershipRatio:    membershipRatio,
			EffectiveRatio:     effectiveRatio,
			CustomRatio:        customRatioPointer,
			HasCustomRatio:     hasCustomRatio,
			MembershipDiscount: membershipDiscount,
		})
	}
	return details, nil
}
