package membership_setting

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

type GroupDiscount struct {
	Group           string  `json:"group"`
	Discount        float64 `json:"discount"`
	StackGroupRatio *bool   `json:"stack_group_ratio"`
}

type Tier struct {
	Id                 string          `json:"id"`
	Name               string          `json:"name"`
	ThresholdAmount    float64         `json:"threshold_amount"`
	AutoUpgradeEnabled bool            `json:"auto_upgrade_enabled"`
	Enabled            bool            `json:"enabled"`
	SortOrder          int             `json:"sort_order"`
	DiscountAllGroups  bool            `json:"discount_all_groups"`
	AllGroupDiscount   float64         `json:"all_group_discount"`
	AllGroupStackRatio *bool           `json:"all_group_stack_ratio"`
	GroupDiscounts     []GroupDiscount `json:"group_discounts"`
}

type TierDiscount struct {
	Tier            Tier
	Multiplier      float64
	StackGroupRatio bool
}

type MembershipSetting struct {
	Enabled bool   `json:"enabled"`
	Tiers   []Tier `json:"tiers"`
}

var membershipSetting = MembershipSetting{
	Enabled: false,
	Tiers:   []Tier{},
}

func init() {
	config.GlobalConfig.Register("membership_setting", &membershipSetting)
}

func GetMembershipSetting() *MembershipSetting {
	return &membershipSetting
}

func IsEnabled() bool {
	return membershipSetting.Enabled
}

func normalizeDiscount(discount float64) float64 {
	if discount <= 0 || discount > 1 {
		return 1
	}
	return discount
}

func boolPtr(value bool) *bool {
	return &value
}

func normalizeStackGroupRatio(value *bool) *bool {
	if value == nil {
		return boolPtr(true)
	}
	return boolPtr(*value)
}

func stackGroupRatioValue(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func normalizeTier(tier Tier) Tier {
	tier.Id = strings.TrimSpace(tier.Id)
	tier.Name = strings.TrimSpace(tier.Name)
	if tier.ThresholdAmount < 0 {
		tier.ThresholdAmount = 0
	}
	tier.AllGroupDiscount = normalizeDiscount(tier.AllGroupDiscount)
	tier.AllGroupStackRatio = normalizeStackGroupRatio(tier.AllGroupStackRatio)
	for i := range tier.GroupDiscounts {
		tier.GroupDiscounts[i].Group = strings.TrimSpace(tier.GroupDiscounts[i].Group)
		tier.GroupDiscounts[i].Discount = normalizeDiscount(tier.GroupDiscounts[i].Discount)
		tier.GroupDiscounts[i].StackGroupRatio = normalizeStackGroupRatio(tier.GroupDiscounts[i].StackGroupRatio)
	}
	return tier
}

func NormalizeTiers(tiers []Tier) []Tier {
	normalized := make([]Tier, 0, len(tiers))
	for _, tier := range tiers {
		tier = normalizeTier(tier)
		if tier.Id == "" || tier.Name == "" {
			continue
		}
		normalized = append(normalized, tier)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].SortOrder == normalized[j].SortOrder {
			return normalized[i].ThresholdAmount < normalized[j].ThresholdAmount
		}
		return normalized[i].SortOrder < normalized[j].SortOrder
	})
	return normalized
}

func GetTiers() []Tier {
	return NormalizeTiers(membershipSetting.Tiers)
}

func GetEnabledTiers() []Tier {
	tiers := GetTiers()
	enabled := make([]Tier, 0, len(tiers))
	for _, tier := range tiers {
		if tier.Enabled {
			enabled = append(enabled, tier)
		}
	}
	return enabled
}

func FindTier(tierId string) (Tier, bool) {
	tierId = strings.TrimSpace(tierId)
	if tierId == "" {
		return Tier{}, false
	}
	for _, tier := range GetTiers() {
		if tier.Id == tierId {
			return tier, true
		}
	}
	return Tier{}, false
}

func ResolveAutoTierByAmount(amount float64) (Tier, bool) {
	var matched Tier
	found := false
	for _, tier := range GetEnabledTiers() {
		if !tier.AutoUpgradeEnabled {
			continue
		}
		if amount+1e-9 < tier.ThresholdAmount {
			continue
		}
		if !found || tier.ThresholdAmount > matched.ThresholdAmount {
			matched = tier
			found = true
		}
	}
	return matched, found
}

func NextTierByAmount(amount float64) (Tier, bool) {
	var next Tier
	found := false
	for _, tier := range GetEnabledTiers() {
		if amount+1e-9 >= tier.ThresholdAmount {
			continue
		}
		if !found || tier.ThresholdAmount < next.ThresholdAmount {
			next = tier
			found = true
		}
	}
	return next, found
}

func GetTierDiscount(tierId string, group string) (TierDiscount, bool) {
	tier, ok := FindTier(tierId)
	if !ok || !tier.Enabled {
		return TierDiscount{}, false
	}
	group = strings.TrimSpace(group)
	discount := 1.0
	stackGroupRatio := true
	matched := false
	if tier.DiscountAllGroups {
		discount = tier.AllGroupDiscount
		stackGroupRatio = stackGroupRatioValue(tier.AllGroupStackRatio)
		matched = true
	}
	for _, item := range tier.GroupDiscounts {
		if item.Group == group {
			discount = item.Discount
			stackGroupRatio = stackGroupRatioValue(item.StackGroupRatio)
			matched = true
			break
		}
	}
	if !matched {
		return TierDiscount{}, false
	}
	return TierDiscount{
		Tier:            tier,
		Multiplier:      normalizeDiscount(discount),
		StackGroupRatio: stackGroupRatio,
	}, true
}

func ParseTiersJSONString(jsonStr string) ([]Tier, error) {
	var tiers []Tier
	if strings.TrimSpace(jsonStr) == "" {
		tiers = []Tier{}
	} else if err := json.Unmarshal([]byte(jsonStr), &tiers); err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(tiers))
	for _, tier := range tiers {
		id := strings.TrimSpace(tier.Id)
		if id == "" {
			return nil, errors.New("membership tier id is empty")
		}
		if seen[id] {
			return nil, errors.New("membership tier id duplicated: " + id)
		}
		seen[id] = true
	}
	return NormalizeTiers(tiers), nil
}

func UpdateTiersByJSONString(jsonStr string) error {
	tiers, err := ParseTiersJSONString(jsonStr)
	if err != nil {
		return err
	}
	membershipSetting.Tiers = tiers
	return nil
}
