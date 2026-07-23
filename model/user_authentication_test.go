package model

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHardDeleteUserPurgesAuthenticationDataWhenRedisFails(t *testing.T) {
	truncateTables(t)

	user := User{Username: "hard-delete-user", Password: "password"}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&Token{UserId: user.Id, Key: "hard-delete-token"}).Error)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}).Error)
	require.NoError(t, DB.Create(&TwoFABackupCode{UserId: user.Id, CodeHash: "hash"}).Error)
	require.NoError(t, DB.Create(&PasskeyCredential{UserID: user.Id, CredentialID: "credential", PublicKey: "public-key"}).Error)
	require.NoError(t, DB.Create(&UserOAuthBinding{UserId: user.Id, ProviderId: 1, ProviderUserId: "provider-user"}).Error)
	require.NoError(t, DB.Create(&UserMembership{UserId: user.Id, TierId: "plus", Source: MembershipSourceManual}).Error)
	ticket := Ticket{UserId: user.Id, Type: TicketTypeTechnical, Status: TicketStatusPending, LastAuthor: TicketAuthorUser}
	require.NoError(t, DB.Create(&ticket).Error)
	require.NoError(t, DB.Create(&TicketMessage{TicketId: ticket.Id, AuthorId: user.Id, AuthorRole: TicketAuthorUser, Content: "encrypted"}).Error)
	setUserMembershipCache(user.Id, "plus", MembershipSourceManual)

	oldRedisEnabled, oldRDB := common.RedisEnabled, common.RDB
	var cacheInvalidatedAfterCommit atomic.Bool
	testRDB := redis.NewClient(&redis.Options{
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			var count int64
			if err := DB.Unscoped().Model(&User{}).Where("id = ?", user.Id).Count(&count).Error; err == nil && count == 0 {
				cacheInvalidatedAfterCommit.Store(true)
			}
			return nil, errors.New("forced redis failure")
		},
		MaxRetries: -1,
	})
	common.RedisEnabled = true
	common.RDB = testRDB
	t.Cleanup(func() {
		_ = testRDB.Close()
		common.RedisEnabled, common.RDB = oldRedisEnabled, oldRDB
	})

	require.NoError(t, HardDeleteUserById(user.Id))
	assert.True(t, cacheInvalidatedAfterCommit.Load())

	var count int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	for _, record := range []any{
		&Token{},
		&TwoFA{},
		&TwoFABackupCode{},
		&PasskeyCredential{},
		&UserOAuthBinding{},
		&UserMembership{},
		&Ticket{},
	} {
		require.NoError(t, DB.Unscoped().Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
		assert.Zero(t, count)
	}
	require.NoError(t, DB.Model(&TicketMessage{}).Where("ticket_id = ?", ticket.Id).Count(&count).Error)
	assert.Zero(t, count)
	_, _, cached := getUserMembershipCache(user.Id)
	assert.False(t, cached)
}

func TestSoftDeleteUserPurgesTicketData(t *testing.T) {
	truncateTables(t)

	user := User{Username: "soft-delete-ticket-user", Password: "password"}
	require.NoError(t, DB.Create(&user).Error)
	ticket := Ticket{UserId: user.Id, Type: TicketTypeOther, Status: TicketStatusPending, LastAuthor: TicketAuthorUser}
	require.NoError(t, DB.Create(&ticket).Error)
	require.NoError(t, DB.Create(&TicketMessage{TicketId: ticket.Id, AuthorId: user.Id, AuthorRole: TicketAuthorUser, Content: "encrypted"}).Error)

	require.NoError(t, DeleteUserById(user.Id))

	var count int64
	require.NoError(t, DB.Unscoped().Model(&Ticket{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&TicketMessage{}).Where("ticket_id = ?", ticket.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestIncrementFailedAttemptsCountsConcurrentFailures(t *testing.T) {
	truncateTables(t)

	user := User{Username: "twofa-cas-user", Password: "password"}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}
	require.NoError(t, DB.Create(&twoFA).Error)

	const attempts = 4
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- (&TwoFA{Id: twoFA.Id}).IncrementFailedAttempts()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var reloaded TwoFA
	require.NoError(t, DB.First(&reloaded, twoFA.Id).Error)
	assert.Equal(t, attempts, reloaded.FailedAttempts)
}

func TestValidateBackupCodeCanOnlySucceedOnce(t *testing.T) {
	truncateTables(t)

	const code = "ABCD-1234"
	require.NoError(t, CreateBackupCodes(123, []string{code}))

	const attempts = 2
	results := make(chan bool, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			valid, err := ValidateBackupCode(123, code)
			results <- valid
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	wins := 0
	for valid := range results {
		if valid {
			wins++
		}
	}
	assert.Equal(t, 1, wins)

	remaining, err := GetUnusedBackupCodeCount(123)
	require.NoError(t, err)
	assert.Zero(t, remaining)
}
