package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// DisableAllChannelKeys changes only key/status metadata and ability availability.
// Accounting counters are never saved from the channel snapshot.
func DisableAllChannelKeys(channelID int) (int, error) {
	lock := GetChannelPollingLock(channelID)
	lock.Lock()
	defer lock.Unlock()
	count := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Acquire a write lock before reading JSON, including on SQLite.
		if err := tx.Model(&Channel{}).Where("id = ?", channelID).UpdateColumn("status", gorm.Expr("status")).Error; err != nil {
			return err
		}
		var channel Channel
		if err := tx.First(&channel, channelID).Error; err != nil {
			return err
		}
		if !channel.ChannelInfo.IsMultiKey {
			return errors.New("channel does not use multiple keys")
		}
		channel.ChannelInfo.MultiKeySize = len(channel.GetKeys())
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = map[int]int{}
		}
		if channel.ChannelInfo.MultiKeyDisabledTime == nil {
			channel.ChannelInfo.MultiKeyDisabledTime = map[int]int64{}
		}
		if channel.ChannelInfo.MultiKeyDisabledReason == nil {
			channel.ChannelInfo.MultiKeyDisabledReason = map[int]string{}
		}
		now := common.GetTimestamp()
		for i := 0; i < channel.ChannelInfo.MultiKeySize; i++ {
			status, exists := channel.ChannelInfo.MultiKeyStatusList[i]
			if !exists || status == common.ChannelStatusEnabled {
				channel.ChannelInfo.MultiKeyStatusList[i] = common.ChannelStatusManuallyDisabled
				channel.ChannelInfo.MultiKeyDisabledTime[i] = now
				channel.ChannelInfo.MultiKeyDisabledReason[i] = ChannelStatusReasonAllKeysDisabled
				count++
			}
		}
		// Preserve a deliberate parent-channel disable and its reason. Enabled
		// parents get the reason used by the existing automatic restore helper.
		if channel.Status == common.ChannelStatusEnabled {
			channel.Status = common.ChannelStatusManuallyDisabled
			info := channel.GetOtherInfo()
			info["status_reason"], info["status_time"] = ChannelStatusReasonAllKeysDisabled, now
			channel.SetOtherInfo(info)
		}
		if err := channel.saveStatusStateTx(tx); err != nil {
			return err
		}
		return tx.Model(&Ability{}).Where("channel_id = ?", channelID).Update("enabled", false).Error
	})
	if err == nil {
		InvalidatePricingCache()
	}
	return count, err
}
