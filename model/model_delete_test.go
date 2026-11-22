package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func preserveModelDeleteOptions(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	original := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	price := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = original
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(price))
	})
}

func modelDeleteFixture(t *testing.T, names string) (*Model, *Channel) {
	t.Helper()
	item := &Model{ModelName: "delete-target", NameRule: NameRuleExact}
	require.NoError(t, DB.Create(item).Error)
	channel := &Channel{Name: "delete-channel", Models: names, Group: "default", Status: common.ChannelStatusEnabled, Type: 1}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	return item, channel
}

func TestDeleteModelMetadataLeavesRelatedDataByDefault(t *testing.T) {
	truncateTables(t)
	preserveModelDeleteOptions(t)
	item, channel := modelDeleteFixture(t, "delete-target,delete-target-extra")
	require.NoError(t, UpdateOption("ModelPrice", `{"delete-target":2,"delete-target-extra":3}`))
	result, err := DeleteModelMetadata([]int{item.Id, item.Id}, false, false)
	require.NoError(t, err)
	require.Equal(t, 1, result.DeletedCount)
	require.Zero(t, result.UpdatedChannels)
	require.NoError(t, DB.First(channel, channel.Id).Error)
	require.Equal(t, "delete-target,delete-target-extra", channel.Models)
	var price Option
	require.NoError(t, DB.Where(&Option{Key: "ModelPrice"}).First(&price).Error)
	require.JSONEq(t, `{"delete-target":2,"delete-target-extra":3}`, price.Value)
}

func TestDeleteModelMetadataLinkedDeletionIsExact(t *testing.T) {
	truncateTables(t)
	preserveModelDeleteOptions(t)
	item, channel := modelDeleteFixture(t, "delete-target,delete-target-extra")
	emptyChannel := &Channel{Name: "empty-after-delete", Models: "delete-target", Group: "default", Status: common.ChannelStatusEnabled, Type: 1}
	require.NoError(t, DB.Create(emptyChannel).Error)
	require.NoError(t, emptyChannel.AddAbilities(nil))
	require.NoError(t, UpdateOption("ModelPrice", `{"delete-target":2,"delete-target-extra":3}`))
	result, err := DeleteModelMetadata([]int{item.Id}, true, true)
	require.NoError(t, err)
	require.Equal(t, ModelDeleteResult{DeletedCount: 1, UpdatedChannels: 2}, result)
	require.NoError(t, DB.First(channel, channel.Id).Error)
	require.Equal(t, "delete-target-extra", channel.Models)
	require.NoError(t, DB.First(emptyChannel, emptyChannel.Id).Error)
	require.Empty(t, emptyChannel.Models)
	var count int64
	require.NoError(t, DB.Model(&Ability{}).Where("model = ? OR model = ?", "delete-target", "").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, DB.Model(&Ability{}).Where("model = ?", "delete-target-extra").Count(&count).Error)
	require.EqualValues(t, 1, count)
	var price Option
	require.NoError(t, DB.Where(&Option{Key: "ModelPrice"}).First(&price).Error)
	require.JSONEq(t, `{"delete-target-extra":3}`, price.Value)
	require.JSONEq(t, price.Value, ratio_setting.ModelPrice2JSONString())
}

func TestDeleteModelMetadataRollsBackRelatedChanges(t *testing.T) {
	truncateTables(t)
	preserveModelDeleteOptions(t)
	item, channel := modelDeleteFixture(t, "delete-target")
	require.NoError(t, DB.Create(&Option{Key: "ModelPrice", Value: `{"delete-target":2,"keep":"invalid"}`}).Error)
	result, err := DeleteModelMetadata([]int{item.Id}, true, true)
	require.Error(t, err)
	require.Equal(t, ModelDeleteResult{}, result)
	require.NoError(t, DB.First(&Model{}, item.Id).Error)
	require.NoError(t, DB.First(channel, channel.Id).Error)
	require.Equal(t, "delete-target", channel.Models)
	var count int64
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ? AND model = ?", channel.Id, "delete-target").Count(&count).Error)
	require.EqualValues(t, 1, count)
	var price Option
	require.NoError(t, DB.Where(&Option{Key: "ModelPrice"}).First(&price).Error)
	require.JSONEq(t, `{"delete-target":2,"keep":"invalid"}`, price.Value)
}

func TestDeleteModelMetadataRejectsMissingAndRuleModels(t *testing.T) {
	truncateTables(t)
	item, _ := modelDeleteFixture(t, "delete-target")
	_, err := DeleteModelMetadata([]int{item.Id, item.Id + 1000000}, false, false)
	require.Error(t, err)
	require.NoError(t, DB.First(&Model{}, item.Id).Error)
	require.NoError(t, DB.Model(item).Update("name_rule", NameRulePrefix).Error)
	_, err = DeleteModelMetadata([]int{item.Id}, true, false)
	require.Error(t, err)
	require.NoError(t, DB.First(&Model{}, item.Id).Error)
}
