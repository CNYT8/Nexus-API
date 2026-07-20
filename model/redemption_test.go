package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchDeleteRedemptions(t *testing.T) {
	truncateTables(t)

	redemptions := []Redemption{
		{Name: "batch-a", Key: "batch-redemption-key-a"},
		{Name: "batch-b", Key: "batch-redemption-key-b"},
		{Name: "batch-c", Key: "batch-redemption-key-c"},
	}
	require.NoError(t, DB.Create(&redemptions).Error)

	count, err := BatchDeleteRedemptions([]int{redemptions[0].Id, redemptions[2].Id})
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	var remaining []Redemption
	require.NoError(t, DB.Order("id ASC").Find(&remaining).Error)
	require.Len(t, remaining, 1)
	assert.Equal(t, redemptions[1].Id, remaining[0].Id)

	var totalWithDeleted int64
	require.NoError(t, DB.Unscoped().Model(&Redemption{}).Count(&totalWithDeleted).Error)
	assert.Equal(t, int64(3), totalWithDeleted)
}

func TestBatchDeleteRedemptionsRejectsEmptyIDs(t *testing.T) {
	count, err := BatchDeleteRedemptions(nil)
	require.Error(t, err)
	assert.Zero(t, count)
}
