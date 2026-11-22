package model

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The guard's values are deliberately stale/partial. Only the subsequent
// statement may provide the complete PostgreSQL configuration snapshot.
func TestRebateConfigPostgresGuardBeforeCompleteSnapshot(t *testing.T) {
	db := newRebateSnapshotDryRunDB(t, "postgres")
	var queries []string
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:config-cap-guard", func(tx *gorm.DB) {
		queries = append(queries, tx.Statement.SQL.String())
		rows := tx.Statement.Dest.(*[]Option)
		if len(queries) == 1 {
			*rows = []Option{{Key: "rebate_setting.inviter_reward_limit_micros", Value: "1000"}}
		} else {
			*rows = []Option{
				{Key: "rebate_setting.inviter_reward_limit_micros", Value: "100"},
				{Key: "rebate_setting.rate_basis_points", Value: "725"},
			}
		}
	}))
	setting, err := GetRebateSettingTx(db)
	require.NoError(t, err)
	require.Len(t, queries, 2)
	require.Contains(t, queries[0], `SELECT "key"`)
	require.Contains(t, queries[0], "FOR SHARE NOWAIT")
	require.Contains(t, queries[0], `ORDER BY "key"`)
	require.NotContains(t, queries[1], "FOR SHARE")
	require.EqualValues(t, 100, setting.InviterRewardLimit)
	require.EqualValues(t, 725, setting.RateBasisPoints)
}

func TestRebateConfigMySQLCurrentReadDoesNotWaitBehindWriter(t *testing.T) {
	db := newRebateSnapshotDryRunDB(t, "mysql")
	var queries []string
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:config-cap-mysql", func(tx *gorm.DB) {
		queries = append(queries, tx.Statement.SQL.String())
	}))
	_, err := GetRebateSettingTx(db)
	require.NoError(t, err)
	require.Len(t, queries, 1)
	require.Contains(t, queries[0], "FOR SHARE NOWAIT")
}

func TestRebateConfigPostgresEmptyOrFailedGuardCannotReadUnguardedConfig(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "failed"}[fail], func(t *testing.T) {
			db := newRebateSnapshotDryRunDB(t, "postgres")
			injected := errors.New("configuration guard unavailable")
			queries := 0
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:config-cap-empty", func(tx *gorm.DB) {
				queries++
				if fail {
					tx.AddError(injected)
				}
			}))
			setting, err := GetRebateSettingTx(db)
			if fail {
				require.ErrorIs(t, err, injected)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, queries)
			require.False(t, setting.Enabled)
			require.Zero(t, setting.RateBasisPoints)
		})
	}
}

func TestRebateConfigCapTighteningAndLegitimateIncrease(t *testing.T) {
	db := setupRebateConfigTest(t)
	require.NoError(t, db.AutoMigrate(&RebateCapBucket{}))
	require.NoError(t, UpdateOption("rebate_setting.inviter_reward_limit_micros", "1000"))
	consume := func(requested, want, limit int64) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			setting, err := GetRebateSettingTx(tx)
			if err != nil {
				return err
			}
			bucket, err := rebateCapBucketTx(tx, 1, RebateCapTypeInviterLifetime, rebateLifetimePeriodKey, setting.InviterRewardLimit)
			if err != nil {
				return err
			}
			require.Equal(t, limit, bucket.LimitMicros)
			allowed := rebateCapAllowed(bucket, requested)
			require.Equal(t, want, allowed)
			_, err = rebateCapConsumeTx(tx, bucket, allowed)
			return err
		}))
	}
	consume(100, 100, 1000)
	require.NoError(t, UpdateOption("rebate_setting.inviter_reward_limit_micros", "100"))
	consume(900, 0, 100)
	// A monotonic-min bucket patch would incorrectly reject this valid raise.
	require.NoError(t, UpdateOption("rebate_setting.inviter_reward_limit_micros", "1000"))
	consume(900, 900, 1000)
	require.NoError(t, UpdateOption("rebate_setting.inviter_reward_limit_micros", "0"))
	consume(900, 900, 0) // Zero retains the existing unlimited-cap semantics.
}

// Uses the existing destructive test-database fixture; DSNs must identify
// dedicated test databases. These checks are skipped unless explicitly set.
func TestRebateConfigCapBarrierRealDialects(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			env := "TEST_POSTGRES_DSN"
			if dialect == "mysql" {
				env = "TEST_MYSQL_DSN"
			}
			dsn := os.Getenv(env)
			if dsn == "" {
				t.Skip(env + " is not set; real database concurrency not tested")
			}
			db := openWalletDialectDB(t, dialect, dsn)
			// Restore the process cache after exercising the production save API.
			useRebateRewardSetting(t, rebateRewardSetting())
			require.NoError(t, UpdateOption("rebate_setting.inviter_reward_limit_micros", "1000"))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			work := db.WithContext(ctx)
			reader := work.Begin()
			require.NoError(t, reader.Error)
			defer reader.Rollback()
			setting, err := GetRebateSettingTx(reader)
			require.NoError(t, err)
			require.EqualValues(t, 1000, setting.InviterRewardLimit)
			// A second accounting reader must be compatible with the first;
			// taking an exclusive reader barrier can deadlock on inviter users.
			other := work.Begin()
			require.NoError(t, other.Error)
			defer other.Rollback()
			_, err = GetRebateSettingTx(other)
			require.NoError(t, err)
			require.NoError(t, other.Rollback().Error)
			oldDB := DB
			DB = work
			defer func() { DB = oldDB }()
			started, saved := make(chan struct{}), make(chan error, 1)
			go func() {
				close(started)
				saved <- UpdateOption("rebate_setting.inviter_reward_limit_micros", "100")
			}()
			<-started
			select {
			case err := <-saved:
				t.Fatalf("configuration save crossed an open accounting snapshot: %v", err)
			case <-time.After(150 * time.Millisecond):
			}
			// The old transaction may finish under its original limit. Only
			// afterwards may the tighter configuration become effective.
			bucket, err := rebateCapBucketTx(reader, 1, RebateCapTypeInviterLifetime, rebateLifetimePeriodKey, setting.InviterRewardLimit)
			require.NoError(t, err)
			_, err = rebateCapConsumeTx(reader, bucket, 100)
			require.NoError(t, err)
			require.NoError(t, reader.Commit().Error)
			select {
			case err := <-saved:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.NoError(t, work.Transaction(func(tx *gorm.DB) error {
				current, err := GetRebateSettingTx(tx)
				if err != nil {
					return err
				}
				bucket, err := rebateCapBucketTx(tx, 1, RebateCapTypeInviterLifetime, rebateLifetimePeriodKey, current.InviterRewardLimit)
				if err != nil {
					return err
				}
				require.EqualValues(t, 100, bucket.LimitMicros)
				require.EqualValues(t, 100, bucket.UsedMicros)
				require.Zero(t, rebateCapAllowed(bucket, 900))
				return nil
			}))
		})
	}
}
