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
)

const (
	MembershipSourceAuto   = "auto"
	MembershipSourceManual = "manual"

	userMembershipCacheTTL int64 = 60
	membershipRatioScale   int64 = 1_000_000_000_000
)

type UserMembership struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"uniqueIndex;not null"`
	TierId    string `json:"tier_id" gorm:"type:varchar(64);default:'';index"`
	Source    string `json:"source" gorm:"type:varchar(32);default:'auto'"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`
}

type MembershipSummary struct {
	UserId           int     `json:"user_id"`
	TierId           string  `json:"tier_id"`
	TierName         string  `json:"tier_name"`
	Source           string  `json:"source"`
	CumulativeAmount float64 `json:"cumulative_amount"`
}

type MembershipDiscountInfo struct {
	Applied         bool    `json:"applied"`
	TierId          string  `json:"tier_id"`
	TierName        string  `json:"tier_name"`
	Discount        float64 `json:"discount"`
	StackGroupRatio bool    `json:"stack_group_ratio"`
}

type userMembershipCacheEntry struct {
	TierId    string
	Source    string
	ExpiresAt int64
}

var userMembershipCache sync.Map

func (membership *UserMembership) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	membership.CreatedAt = now
	membership.UpdatedAt = now
	if membership.Source == "" {
		membership.Source = MembershipSourceAuto
	}
	return nil
}

func (membership *UserMembership) BeforeUpdate(tx *gorm.DB) error {
	membership.UpdatedAt = common.GetTimestamp()
	if membership.Source == "" {
		membership.Source = MembershipSourceAuto
	}
	return nil
}

func getUserMembershipCache(userId int) (tierId string, source string, ok bool) {
	if userId <= 0 {
		return "", "", false
	}
	value, ok := userMembershipCache.Load(userId)
	if !ok {
		return "", "", false
	}
	entry, ok := value.(userMembershipCacheEntry)
	if !ok || entry.ExpiresAt <= common.GetTimestamp() {
		userMembershipCache.Delete(userId)
		return "", "", false
	}
	return entry.TierId, entry.Source, true
}

func setUserMembershipCache(userId int, tierId string, source string) {
	if userId <= 0 {
		return
	}
	userMembershipCache.Store(userId, userMembershipCacheEntry{
		TierId:    tierId,
		Source:    source,
		ExpiresAt: common.GetTimestamp() + userMembershipCacheTTL,
	})
}

func invalidateUserMembershipCache(userId int) {
	if userId <= 0 {
		return
	}
	userMembershipCache.Delete(userId)
}

func getUserMembershipDirect(userId int) (*UserMembership, error) {
	if userId <= 0 {
		return nil, errors.New("user id is empty")
	}
	membership := &UserMembership{}
	err := DB.Where("user_id = ?", userId).First(membership).Error
	if err != nil {
		return nil, err
	}
	return membership, nil
}

func GetUserMembershipTier(userId int) (tierId string, source string) {
	if !membership_setting.IsEnabled() || userId <= 0 {
		return "", ""
	}
	if tierId, source, ok := getUserMembershipCache(userId); ok {
		return tierId, source
	}

	membership, err := getUserMembershipDirect(userId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := AutoUpgradeUserMembership(userId); err != nil {
			common.SysLog(fmt.Sprintf("failed to auto upgrade membership for user %d: %s", userId, err.Error()))
		}
		membership, err = getUserMembershipDirect(userId)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		setUserMembershipCache(userId, "", "")
		return "", ""
	}
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to get membership for user %d: %s", userId, err.Error()))
		setUserMembershipCache(userId, "", "")
		return "", ""
	}
	setUserMembershipCache(userId, membership.TierId, membership.Source)
	return membership.TierId, membership.Source
}

func SetUserMembershipTier(userId int, tierId string, source string) error {
	if userId <= 0 {
		return errors.New("user id is empty")
	}
	tierId = strings.TrimSpace(tierId)
	if err := ValidateMembershipTierId(tierId); err != nil {
		return err
	}
	if source == "" {
		source = MembershipSourceManual
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		if tierId == "" {
			return tx.Where("user_id = ?", userId).Delete(&UserMembership{}).Error
		}
		membership := &UserMembership{}
		err := tx.Where("user_id = ?", userId).First(membership).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			membership = &UserMembership{
				UserId: userId,
				TierId: tierId,
				Source: source,
			}
			return tx.Create(membership).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(membership).Updates(map[string]interface{}{
			"tier_id": tierId,
			"source":  source,
		}).Error
	})
	if err == nil {
		invalidateUserMembershipCache(userId)
	}
	return err
}

func ValidateMembershipTierId(tierId string) error {
	tierId = strings.TrimSpace(tierId)
	if tierId == "" {
		return nil
	}
	if _, ok := membership_setting.FindTier(tierId); !ok {
		return errors.New("membership tier not found")
	}
	return nil
}

func GetUserCumulativeTopUpAmount(userId int) (float64, error) {
	if userId <= 0 {
		return 0, errors.New("user id is empty")
	}
	quotaPerUnit := common.QuotaPerUnit
	if quotaPerUnit <= 0 {
		quotaPerUnit = 1
	}
	var total float64
	err := DB.Model(&TopUp{}).
		Where("user_id = ? AND status = ?", userId, common.TopUpStatusSuccess).
		Select(
			"COALESCE(SUM(CASE WHEN payment_provider = ? THEN amount / ? WHEN payment_provider = ? THEN money ELSE amount END), 0)",
			PaymentProviderCreem,
			quotaPerUnit,
			PaymentProviderStripe,
		).
		Scan(&total).Error
	if err != nil {
		return 0, err
	}
	return total, nil
}

func AutoUpgradeUserMembership(userId int) error {
	if !membership_setting.IsEnabled() || userId <= 0 {
		return nil
	}
	cumulativeAmount, err := GetUserCumulativeTopUpAmount(userId)
	if err != nil {
		return err
	}
	targetTier, ok := membership_setting.ResolveAutoTierByAmount(cumulativeAmount)
	if !ok {
		return nil
	}

	currentMembership, err := getUserMembershipDirect(userId)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	currentThreshold := -1.0
	if currentMembership != nil && currentMembership.TierId != "" {
		if currentTier, ok := membership_setting.FindTier(currentMembership.TierId); ok {
			currentThreshold = currentTier.ThresholdAmount
		}
	}
	if currentMembership != nil && currentMembership.TierId == targetTier.Id {
		return nil
	}
	if currentThreshold >= targetTier.ThresholdAmount {
		return nil
	}
	return SetUserMembershipTier(userId, targetTier.Id, MembershipSourceAuto)
}

func AutoUpgradeUserMembershipAfterTopUp(userId int) {
	if err := AutoUpgradeUserMembership(userId); err != nil {
		common.SysLog(fmt.Sprintf("failed to auto upgrade membership after topup for user %d: %s", userId, err.Error()))
	}
}

func normalizeMembershipRatio(ratio float64) float64 {
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return ratio
	}
	return math.Round(ratio*float64(membershipRatioScale)) / float64(membershipRatioScale)
}

func ApplyMembershipDiscount(userId int, group string, groupRatio float64) (float64, MembershipDiscountInfo) {
	info := MembershipDiscountInfo{Discount: 1}
	if !membership_setting.IsEnabled() || userId <= 0 {
		return groupRatio, info
	}
	tierId, _ := GetUserMembershipTier(userId)
	if tierId == "" {
		return groupRatio, info
	}
	tierDiscount, ok := membership_setting.GetTierDiscount(tierId, group)
	if !ok || tierDiscount.Multiplier <= 0 || tierDiscount.Multiplier >= 1 {
		return groupRatio, info
	}
	finalRatio := tierDiscount.Multiplier
	if tierDiscount.StackGroupRatio {
		finalRatio = groupRatio * tierDiscount.Multiplier
	}
	finalRatio = normalizeMembershipRatio(finalRatio)
	info = MembershipDiscountInfo{
		Applied:         true,
		TierId:          tierDiscount.Tier.Id,
		TierName:        tierDiscount.Tier.Name,
		Discount:        tierDiscount.Multiplier,
		StackGroupRatio: tierDiscount.StackGroupRatio,
	}
	return finalRatio, info
}

func BuildMembershipSummary(userId int) (MembershipSummary, error) {
	summary := MembershipSummary{UserId: userId}
	if userId <= 0 {
		return summary, errors.New("user id is empty")
	}
	if !membership_setting.IsEnabled() {
		return summary, nil
	}
	cumulativeAmount, err := GetUserCumulativeTopUpAmount(userId)
	if err != nil {
		return summary, err
	}
	summary.CumulativeAmount = cumulativeAmount
	tierId, source := GetUserMembershipTier(userId)
	summary.TierId = tierId
	summary.Source = source
	if tierId != "" {
		if tier, ok := membership_setting.FindTier(tierId); ok {
			summary.TierName = tier.Name
		}
	}
	return summary, nil
}

func AttachUserMemberships(users []*User) {
	if !membership_setting.IsEnabled() {
		return
	}
	if len(users) == 0 {
		return
	}
	ids := make([]int, 0, len(users))
	userById := make(map[int]*User, len(users))
	for _, user := range users {
		if user == nil || user.Id <= 0 {
			continue
		}
		ids = append(ids, user.Id)
		userById[user.Id] = user
	}
	if len(ids) == 0 {
		return
	}

	tierNames := make(map[string]string)
	for _, tier := range membership_setting.GetTiers() {
		tierNames[tier.Id] = tier.Name
	}

	var memberships []UserMembership
	if err := DB.Where("user_id IN ?", ids).Find(&memberships).Error; err != nil {
		common.SysLog("failed to attach user memberships: " + err.Error())
		return
	}
	for _, membership := range memberships {
		user, ok := userById[membership.UserId]
		if !ok {
			continue
		}
		user.MembershipTierId = membership.TierId
		user.MembershipName = tierNames[membership.TierId]
		user.MembershipSource = membership.Source
	}
}
