package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	membership_setting "github.com/QuantumNous/new-api/setting/membership_setting"
	"github.com/gin-gonic/gin"
)

func GetMembershipSelf(c *gin.Context) {
	userId := c.GetInt("id")
	if !membership_setting.IsEnabled() {
		common.ApiSuccess(c, gin.H{
			"enabled":       false,
			"tiers":         []membership_setting.Tier{},
			"current":       model.MembershipSummary{UserId: userId},
			"next_tier":     nil,
			"has_next_tier": false,
		})
		return
	}

	if err := model.AutoUpgradeUserMembership(userId); err != nil {
		common.SysLog("failed to sync user membership: " + err.Error())
	}

	summary, err := model.BuildMembershipSummary(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	nextTier, hasNextTier := membership_setting.NextTierByAmount(summary.CumulativeAmount)

	common.ApiSuccess(c, gin.H{
		"enabled":       membership_setting.IsEnabled(),
		"tiers":         membership_setting.GetEnabledTiers(),
		"current":       summary,
		"next_tier":     nextTier,
		"has_next_tier": hasNextTier,
	})
}

func AdminGetMembershipTiers(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"enabled": membership_setting.IsEnabled(),
		"tiers":   membership_setting.GetTiers(),
	})
}
