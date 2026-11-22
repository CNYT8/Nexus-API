package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func createManagedSessionForTest(t *testing.T, user *User, agent string) (string, *UserSession) {
	t.Helper()
	secret, err := CreateUserSession(user, "password", "192.0.2.1", agent)
	require.NoError(t, err)
	_, session, err := ValidateUserSession(secret, user.Id)
	require.NoError(t, err)
	return secret, session
}

func TestManagedUserSessionsOwnerIsolation(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleCommonUser)
	other := &User{Username: "managed-other", Password: "password", AffCode: "managed-other", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(other).Error)
	currentSecret, current := createManagedSessionForTest(t, user, "current")
	ownSecret, own := createManagedSessionForTest(t, user, "own-device")
	foreignSecret, foreign := createManagedSessionForTest(t, other, "foreign-device")

	list, err := ListUserSessions(user.Id, current.Id)
	require.NoError(t, err)
	require.Len(t, list.Items, 2)
	require.Equal(t, current.Id, list.Items[0].Id)
	require.True(t, list.Items[0].Current)
	require.Equal(t, own.Id, list.Items[1].Id)
	require.False(t, list.Items[1].Current)

	isCurrent, err := RevokeUserSessionByID(user.Id, current.Id, foreign.Id)
	require.ErrorIs(t, err, ErrManagedSessionNotActive)
	require.False(t, isCurrent)
	_, err = ListUserSessions(user.Id, foreign.Id)
	require.ErrorIs(t, err, ErrAuthStateInvalid)
	_, err = RevokeUserSessionByID(user.Id, foreign.Id, own.Id)
	require.ErrorIs(t, err, ErrAuthStateInvalid)
	count, err := RevokeOtherUserSessions(user.Id, foreign.Id)
	require.ErrorIs(t, err, ErrAuthStateInvalid)
	require.Zero(t, count)

	for _, secret := range []string{currentSecret, ownSecret} {
		_, _, err = ValidateUserSession(secret, user.Id)
		require.NoError(t, err)
	}
	_, _, err = ValidateUserSession(foreignSecret, other.Id)
	require.NoError(t, err)
}

func TestManagedUserSessionsRepeatedRevocationRejected(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleCommonUser)
	currentSecret, current := createManagedSessionForTest(t, user, "current")
	targetSecret, target := createManagedSessionForTest(t, user, "target")

	isCurrent, err := RevokeUserSessionByID(user.Id, current.Id, target.Id)
	require.NoError(t, err)
	require.False(t, isCurrent)
	var revoked UserSession
	require.NoError(t, DB.First(&revoked, target.Id).Error)
	require.Positive(t, revoked.RevokedAt)
	isCurrent, err = RevokeUserSessionByID(user.Id, current.Id, target.Id)
	require.ErrorIs(t, err, ErrManagedSessionNotActive)
	require.False(t, isCurrent)
	var unchanged UserSession
	require.NoError(t, DB.First(&unchanged, target.Id).Error)
	require.Equal(t, revoked.RevokedAt, unchanged.RevokedAt)
	_, _, err = ValidateUserSession(targetSecret, user.Id)
	require.Error(t, err)
	_, _, err = ValidateUserSession(currentSecret, user.Id)
	require.NoError(t, err)

	isCurrent, err = RevokeUserSessionByID(user.Id, current.Id, current.Id)
	require.NoError(t, err)
	require.True(t, isCurrent)
	_, err = ListUserSessions(user.Id, current.Id)
	require.ErrorIs(t, err, ErrAuthStateInvalid)
}

func TestManagedUserSessionsRevokeOthersPreservesCurrent(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleCommonUser)
	other := &User{Username: "managed-other", Password: "password", AffCode: "managed-other", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(other).Error)
	currentSecret, current := createManagedSessionForTest(t, user, "current")
	firstSecret, _ := createManagedSessionForTest(t, user, "first")
	secondSecret, _ := createManagedSessionForTest(t, user, "second")
	foreignSecret, _ := createManagedSessionForTest(t, other, "foreign")

	count, err := RevokeOtherUserSessions(user.Id, current.Id)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	for _, secret := range []string{firstSecret, secondSecret} {
		_, _, err = ValidateUserSession(secret, user.Id)
		require.Error(t, err)
	}
	_, _, err = ValidateUserSession(currentSecret, user.Id)
	require.NoError(t, err)
	_, _, err = ValidateUserSession(foreignSecret, other.Id)
	require.NoError(t, err)
	count, err = RevokeOtherUserSessions(user.Id, current.Id)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestManagedUserSessionsRejectInactiveCurrent(t *testing.T) {
	for _, state := range []string{"expired", "invalidated", "revoked", "disabled"} {
		t.Run(state, func(t *testing.T) {
			user := setupAuthenticationRegressionTest(t, common.RoleCommonUser)
			_, current := createManagedSessionForTest(t, user, "current")
			_, target := createManagedSessionForTest(t, user, "target")
			switch state {
			case "expired":
				require.NoError(t, DB.Model(current).Update("expires_at", time.Now().Unix()-60).Error)
			case "invalidated":
				require.NoError(t, DB.Model(user).Update("auth_version", user.AuthVersion+1).Error)
			case "revoked":
				require.NoError(t, DB.Model(current).Update("revoked_at", time.Now().Unix()-60).Error)
			case "disabled":
				require.NoError(t, DB.Model(user).Update("status", common.UserStatusDisabled).Error)
			}
			_, err := ListUserSessions(user.Id, current.Id)
			require.ErrorIs(t, err, ErrAuthStateInvalid)
			isCurrent, err := RevokeUserSessionByID(user.Id, current.Id, target.Id)
			require.ErrorIs(t, err, ErrAuthStateInvalid)
			require.False(t, isCurrent)
			count, err := RevokeOtherUserSessions(user.Id, current.Id)
			require.ErrorIs(t, err, ErrAuthStateInvalid)
			require.Zero(t, count)
			var untouched UserSession
			require.NoError(t, DB.First(&untouched, target.Id).Error)
			require.Zero(t, untouched.RevokedAt)
		})
	}
}

func TestManagedUserSessionsInactiveTargetsRemainUnchanged(t *testing.T) {
	user := setupAuthenticationRegressionTest(t, common.RoleCommonUser)
	_, current := createManagedSessionForTest(t, user, "current")
	_, expired := createManagedSessionForTest(t, user, "expired")
	_, invalidated := createManagedSessionForTest(t, user, "invalidated")
	_, revoked := createManagedSessionForTest(t, user, "revoked")
	revokedAt := time.Now().Unix() - 60
	require.NoError(t, DB.Model(expired).Update("expires_at", revokedAt).Error)
	require.NoError(t, DB.Model(invalidated).Update("auth_version", user.AuthVersion-1).Error)
	require.NoError(t, DB.Model(revoked).Update("revoked_at", revokedAt).Error)

	list, err := ListUserSessions(user.Id, current.Id)
	require.NoError(t, err)
	statuses := make(map[int64]string)
	for _, item := range list.Items {
		statuses[item.Id] = item.Status
	}
	require.Equal(t, map[int64]string{
		current.Id: "active", expired.Id: "expired", invalidated.Id: "invalidated", revoked.Id: "revoked",
	}, statuses)
	for _, id := range []int64{expired.Id, invalidated.Id, revoked.Id} {
		isCurrent, err := RevokeUserSessionByID(user.Id, current.Id, id)
		require.ErrorIs(t, err, ErrManagedSessionNotActive)
		require.False(t, isCurrent)
	}
	count, err := RevokeOtherUserSessions(user.Id, current.Id)
	require.NoError(t, err)
	require.Zero(t, count)
	for _, id := range []int64{current.Id, expired.Id, invalidated.Id, revoked.Id} {
		var got UserSession
		require.NoError(t, DB.First(&got, id).Error)
		if id == revoked.Id {
			require.Equal(t, revokedAt, got.RevokedAt)
		} else {
			require.Zero(t, got.RevokedAt)
		}
	}
}
