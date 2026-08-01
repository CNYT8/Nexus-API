package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebRiskChallengesOnThirdDistinctIPWithinWindow(t *testing.T) {
	truncateTables(t)
	originalSecret := common.CryptoSecret
	common.CryptoSecret = "web-risk-test-secret"
	t.Cleanup(func() { common.CryptoSecret = originalSecret })

	status, err := observeWebRiskIPAt(701, "203.0.113.1", 1_000)
	require.NoError(t, err)
	assert.False(t, status.Challenged)
	assert.Equal(t, 1, status.DistinctIPs)

	status, err = observeWebRiskIPAt(701, "203.0.113.1", 1_060)
	require.NoError(t, err)
	assert.False(t, status.Challenged)
	assert.Equal(t, 1, status.DistinctIPs)

	status, err = observeWebRiskIPAt(701, "198.51.100.2", 1_120)
	require.NoError(t, err)
	assert.False(t, status.Challenged)
	assert.Equal(t, 2, status.DistinctIPs)

	status, err = observeWebRiskIPAt(701, "192.0.2.3", 1_299)
	require.NoError(t, err)
	assert.True(t, status.Challenged)
	assert.Equal(t, 3, status.DistinctIPs)

	var stored WebRiskState
	require.NoError(t, DB.First(&stored, "user_id = ?", 701).Error)
	assert.NotContains(t, stored.RecentIPs, "203.0.113.1")
	assert.NotContains(t, stored.RecentIPs, "198.51.100.2")
	assert.NotContains(t, stored.RecentIPs, "192.0.2.3")
}

func TestWebRiskDistinctIPWindowExpires(t *testing.T) {
	truncateTables(t)

	_, err := observeWebRiskIPAt(702, "203.0.113.1", 2_000)
	require.NoError(t, err)
	_, err = observeWebRiskIPAt(702, "198.51.100.2", 2_301)
	require.NoError(t, err)
	status, err := observeWebRiskIPAt(702, "192.0.2.3", 2_302)
	require.NoError(t, err)

	assert.False(t, status.Challenged)
	assert.Equal(t, 2, status.DistinctIPs)
}

func TestWebRiskChallengePersistsUntilAccountReset(t *testing.T) {
	truncateTables(t)

	for index, ip := range []string{"203.0.113.1", "198.51.100.2", "192.0.2.3"} {
		_, err := observeWebRiskIPAt(703, ip, int64(3_000+index))
		require.NoError(t, err)
	}

	status, err := GetWebRiskStatus(703)
	require.NoError(t, err)
	assert.True(t, status.Challenged)

	status, err = observeWebRiskIPAt(703, "192.0.2.99", 4_000)
	require.NoError(t, err)
	assert.True(t, status.Challenged)

	require.NoError(t, ResetWebRisk(703))
	status, err = GetWebRiskStatus(703)
	require.NoError(t, err)
	assert.False(t, status.Challenged)
	assert.Zero(t, status.DistinctIPs)
}

func TestWebRiskRejectsEmptyIPWithoutPersistingState(t *testing.T) {
	truncateTables(t)

	_, err := observeWebRiskIPAt(704, strings.Repeat(" ", 3), 5_000)
	require.Error(t, err)

	var count int64
	require.NoError(t, DB.Model(&WebRiskState{}).Where("user_id = ?", 704).Count(&count).Error)
	assert.Zero(t, count)
}

func TestWebRiskFingerprintSecretPersistsAcrossCryptoSecretChanges(t *testing.T) {
	truncateTables(t)
	originalSecret := common.CryptoSecret
	t.Cleanup(func() {
		common.CryptoSecret = originalSecret
		common.SetWebRiskFingerprintSecret("")
	})

	common.CryptoSecret = "first-process-secret"
	require.NoError(t, initializeWebRiskFingerprintSecret())
	firstSecret := common.GetWebRiskFingerprintSecret()
	require.NotEmpty(t, firstSecret)
	firstHash, err := WebRiskIPFingerprint("203.0.113.1")
	require.NoError(t, err)

	common.CryptoSecret = "second-process-secret"
	common.SetWebRiskFingerprintSecret("")
	require.NoError(t, initializeWebRiskFingerprintSecret())
	require.Equal(t, firstSecret, common.GetWebRiskFingerprintSecret())
	secondHash, err := WebRiskIPFingerprint("203.0.113.1")
	require.NoError(t, err)
	require.Equal(t, firstHash, secondHash)
}
