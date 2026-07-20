package controller

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRedemptionControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Redemption{}))
	return db
}

func TestDeleteRedemptionBatch(t *testing.T) {
	db := setupRedemptionControllerTestDB(t)
	redemptions := []model.Redemption{
		{Name: "batch-a", Key: "controller-redemption-key-a"},
		{Name: "batch-b", Key: "controller-redemption-key-b"},
		{Name: "batch-c", Key: "controller-redemption-key-c"},
	}
	require.NoError(t, db.Create(&redemptions).Error)

	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/redemption/batch", RedemptionBatch{
		Ids: []int{redemptions[0].Id, redemptions[2].Id},
	}, 1)
	DeleteRedemptionBatch(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var deletedCount int64
	require.NoError(t, json.Unmarshal(response.Data, &deletedCount))
	assert.Equal(t, int64(2), deletedCount)

	var remaining int64
	require.NoError(t, db.Model(&model.Redemption{}).Count(&remaining).Error)
	assert.Equal(t, int64(1), remaining)
}

func TestDeleteRedemptionBatchRejectsInvalidIDs(t *testing.T) {
	tests := []struct {
		name string
		ids  []int
	}{
		{name: "empty", ids: nil},
		{name: "non-positive", ids: []int{0}},
		{name: "too many", ids: make([]int, maxRedemptionBatchDeleteSize+1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/redemption/batch", RedemptionBatch{Ids: test.ids}, 1)
			DeleteRedemptionBatch(ctx)
			response := decodeAPIResponse(t, recorder)
			assert.False(t, response.Success)
		})
	}
}
