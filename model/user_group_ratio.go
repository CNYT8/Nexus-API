package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	membership_setting "github.com/QuantumNous/new-api/setting/membership_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const userGroupRatioCacheTTL int64 = 60

type UserGroupRatioOverride struct {
	Id        int     `json:"id"`
	UserId    int     `json:"user_id" gorm:"not null;uniqueIndex:idx_user_group_ratio_override"`
	Group     string  `json:"group" gorm:"type:varchar(64);not null;uniqueIndex:idx_user_group_ratio_override"`
	Ratio     float64 `json:"ratio" gorm:"type:decimal(24,12);not null"`
	CreatedAt int64   `json:"created_at" gorm:"bigint"`
	UpdatedAt int64   `json:"updated_at" gorm:"bigint"`
}

type userGroupRatioCacheKey struct {
	UserId int
	Group  string
}

type userGroupRatioCacheEntry struct {
	Ratio     float64
	Found     bool
	ExpiresAt int64
}

var userGroupRatioCache sync.Map

func normalizeUserGroupRatio(ratio float64) float64 {
	return math.Round(ratio*float64(membershipRatioScale)) / float64(membershipRatioScale)
}

func ValidateUserGroupRatio(group string, ratio float64) error {
	group = strings.TrimSpace(group)
	if group == "" || len(group) > 64 {
		return errors.New("group is invalid")
	}
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > membership_setting.MaxMembershipMultiplier {
		return fmt.Errorf("group ratio must be between 0 and %d", membership_setting.MaxMembershipMultiplier)
	}
	return nil
}

func userGroupRatioKey(userId int, group string) userGroupRatioCacheKey {
	return userGroupRatioCacheKey{UserId: userId, Group: strings.TrimSpace(group)}
}

func invalidateUserGroupRatioCache(userId int, group string) {
	userGroupRatioCache.Delete(userGroupRatioKey(userId, group))
}

func invalidateUserGroupRatioCacheForUser(userId int) {
	userGroupRatioCache.Range(func(key, _ interface{}) bool {
		cacheKey, ok := key.(userGroupRatioCacheKey)
		if ok && cacheKey.UserId == userId {
			userGroupRatioCache.Delete(key)
		}
		return true
	})
}

func getUserGroupRatioOverride(userId int, group string) (float64, bool, error) {
	if userId <= 0 || strings.TrimSpace(group) == "" {
		return 0, false, nil
	}
	key := userGroupRatioKey(userId, group)
	if cached, ok := userGroupRatioCache.Load(key); ok {
		entry := cached.(userGroupRatioCacheEntry)
		if entry.ExpiresAt > common.GetTimestamp() {
			return entry.Ratio, entry.Found, nil
		}
		userGroupRatioCache.Delete(key)
	}

	override := UserGroupRatioOverride{}
	err := DB.Where(&UserGroupRatioOverride{UserId: userId, Group: key.Group}).First(&override).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		userGroupRatioCache.Store(key, userGroupRatioCacheEntry{
			Found:     false,
			ExpiresAt: common.GetTimestamp() + userGroupRatioCacheTTL,
		})
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}

	ratio := normalizeUserGroupRatio(override.Ratio)
	userGroupRatioCache.Store(key, userGroupRatioCacheEntry{
		Ratio:     ratio,
		Found:     true,
		ExpiresAt: common.GetTimestamp() + userGroupRatioCacheTTL,
	})
	return ratio, true, nil
}

func ListUserGroupRatioOverrides(userId int) (map[string]float64, error) {
	result := make(map[string]float64)
	if userId <= 0 {
		return result, errors.New("user id is invalid")
	}
	var overrides []UserGroupRatioOverride
	if err := DB.Where("user_id = ?", userId).Find(&overrides).Error; err != nil {
		return nil, err
	}
	for _, override := range overrides {
		result[override.Group] = normalizeUserGroupRatio(override.Ratio)
	}
	return result, nil
}

func SetUserGroupRatioOverride(userId int, group string, ratio float64) error {
	group = strings.TrimSpace(group)
	if userId <= 0 {
		return errors.New("user id is invalid")
	}
	if err := ValidateUserGroupRatio(group, ratio); err != nil {
		return err
	}
	ratio = normalizeUserGroupRatio(ratio)
	now := common.GetTimestamp()
	override := UserGroupRatioOverride{
		UserId:    userId,
		Group:     group,
		Ratio:     ratio,
		CreatedAt: now,
		UpdatedAt: now,
	}
	err := upsertUserGroupRatioOverride(DB, &override).Error
	if err == nil {
		invalidateUserGroupRatioCache(userId, group)
	}
	return err
}

func upsertUserGroupRatioOverride(db *gorm.DB, override *UserGroupRatioOverride) *gorm.DB {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "group"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"ratio":      override.Ratio,
			"updated_at": override.UpdatedAt,
		}),
	}).Create(override)
}

func DeleteUserGroupRatioOverride(userId int, group string) error {
	group = strings.TrimSpace(group)
	if userId <= 0 || group == "" {
		return errors.New("user id or group is invalid")
	}
	err := DB.Where(&UserGroupRatioOverride{UserId: userId, Group: group}).Delete(&UserGroupRatioOverride{}).Error
	if err == nil {
		invalidateUserGroupRatioCache(userId, group)
	}
	return err
}

// ResolveUserGroupRatio applies membership first, then an optional user-level
// override. The override is final, so admin display and billing stay identical.
func ResolveUserGroupRatio(userId int, group string, groupRatio float64) (float64, MembershipDiscountInfo, bool) {
	membershipRatio, membershipInfo := ApplyMembershipDiscount(userId, group, groupRatio)
	overrideRatio, found, err := getUserGroupRatioOverride(userId, group)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to get group ratio override for user %d group %s: %s", userId, group, err.Error()))
		return membershipRatio, membershipInfo, false
	}
	if found {
		return overrideRatio, membershipInfo, true
	}
	return membershipRatio, membershipInfo, false
}
