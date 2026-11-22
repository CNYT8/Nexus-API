package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionSQLiteMigrationPreservesLegacyOverflow(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "new_database"
		if legacy {
			name = "existing_database"
		}
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			pool.SetMaxOpenConns(1)
			previousDB, previousSQLite := DB, common.UsingSQLite
			DB, common.UsingSQLite = db, true
			t.Cleanup(func() {
				DB, common.UsingSQLite = previousDB, previousSQLite
				_ = pool.Close()
			})

			if legacy {
				require.NoError(t, db.Exec("CREATE TABLE subscription_plans (id integer PRIMARY KEY, title varchar(128) NOT NULL, price_amount decimal(10,6) NOT NULL)").Error)
				require.NoError(t, db.Exec("INSERT INTO subscription_plans (id, title, price_amount) VALUES (1, 'legacy', 9.99)").Error)
			}
			require.NoError(t, ensureSubscriptionPlanTableSQLite())
			require.NoError(t, ensureSubscriptionPlanTableSQLite())
			require.True(t, db.Migrator().HasColumn(&SubscriptionPlan{}, "allow_wallet_overflow"))
			require.True(t, db.Migrator().HasColumn(&SubscriptionPlan{}, "downgrade_group"))
			if legacy {
				var row struct {
					Title               string
					AllowWalletOverflow *bool
					DowngradeGroup      string
				}
				require.NoError(t, db.Table("subscription_plans").Where("id = ?", 1).Take(&row).Error)
				require.Equal(t, "legacy", row.Title)
				require.Nil(t, row.AllowWalletOverflow)
				require.Empty(t, row.DowngradeGroup)
			}

			allowOverflow := false
			plan := SubscriptionPlan{Title: "explicit-policy", PriceAmount: 2, AllowWalletOverflow: &allowOverflow, DowngradeGroup: "default"}
			require.NoError(t, db.Create(&plan).Error)
			var saved SubscriptionPlan
			require.NoError(t, db.First(&saved, plan.Id).Error)
			require.NotNil(t, saved.AllowWalletOverflow)
			require.False(t, *saved.AllowWalletOverflow)
			require.Equal(t, "default", saved.DowngradeGroup)
			require.NoError(t, db.Model(&saved).Updates(map[string]interface{}{"allow_wallet_overflow": true, "downgrade_group": "vip"}).Error)
			require.NoError(t, db.First(&saved, plan.Id).Error)
			require.True(t, *saved.AllowWalletOverflow)
			require.Equal(t, "vip", saved.DowngradeGroup)
		})
	}
}
