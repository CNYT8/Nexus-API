package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationCleanupRetainsReplayProtectionWindow(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleCommonUser)
	now := time.Date(2026, time.November, 21, 0, 0, 0, 0, time.UTC)
	flowCutoff := now.Add(-24 * time.Hour).Unix()
	sessionCutoff := now.Add(-UserSessionLifetime).Unix()

	flows := []AuthFlow{
		{SecretHash: "cleanup-flow-old", Purpose: "login", ExpiresAt: flowCutoff - 1},
		{SecretHash: "cleanup-flow-boundary", Purpose: "login", ExpiresAt: flowCutoff},
		{SecretHash: "cleanup-flow-active", Purpose: "login", ExpiresAt: now.Add(time.Minute).Unix()},
	}
	sessions := []UserSession{
		{UserId: user.Id, SecretHash: "cleanup-session-old", AuthVersion: 1, ExpiresAt: sessionCutoff - 1, RevokedAt: sessionCutoff - 2},
		{UserId: user.Id, SecretHash: "cleanup-session-boundary", AuthVersion: 1, ExpiresAt: sessionCutoff, RevokedAt: sessionCutoff - 1},
		{UserId: user.Id, SecretHash: "cleanup-session-recent-tombstone", AuthVersion: 1, ExpiresAt: now.Add(-time.Hour).Unix(), RevokedAt: now.Add(-time.Hour).Unix()},
		{UserId: user.Id, SecretHash: "cleanup-session-active", AuthVersion: 1, ExpiresAt: now.Add(time.Hour).Unix()},
	}
	limits := []AccountSecurityLimit{
		{BucketHash: "cleanup-limit-old", Attempts: 5, ExpiresAt: now.Unix() - 1},
		{BucketHash: "cleanup-limit-boundary", Attempts: 5, ExpiresAt: now.Unix()},
		{BucketHash: "cleanup-limit-active", Attempts: 5, ExpiresAt: now.Unix() + 1},
	}
	require.NoError(t, DB.Create(&flows).Error)
	require.NoError(t, DB.Create(&sessions).Error)
	require.NoError(t, DB.Create(&limits).Error)
	require.NoError(t, DeleteExpiredAuthenticationStateWithContext(context.Background(), now))

	var flowHashes, sessionHashes, limitHashes []string
	require.NoError(t, DB.Model(&AuthFlow{}).Pluck("secret_hash", &flowHashes).Error)
	require.NoError(t, DB.Model(&UserSession{}).Pluck("secret_hash", &sessionHashes).Error)
	require.NoError(t, DB.Model(&AccountSecurityLimit{}).Pluck("bucket_hash", &limitHashes).Error)
	require.ElementsMatch(t, []string{"cleanup-flow-boundary", "cleanup-flow-active"}, flowHashes)
	require.ElementsMatch(t, []string{"cleanup-session-boundary", "cleanup-session-recent-tombstone", "cleanup-session-active"}, sessionHashes)
	require.ElementsMatch(t, []string{"cleanup-limit-boundary", "cleanup-limit-active"}, limitHashes)
}

func TestAuthenticationCleanupHonorsCancellation(t *testing.T) {
	setupAuthenticationRegressionTest(t, common.RoleCommonUser)
	flow := AuthFlow{SecretHash: "cleanup-cancelled", Purpose: "login", ExpiresAt: 1}
	require.NoError(t, DB.Create(&flow).Error)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, DeleteExpiredAuthenticationStateWithContext(ctx, time.Now()), context.Canceled)
	var count int64
	require.NoError(t, DB.Model(&AuthFlow{}).Where("id = ?", flow.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
