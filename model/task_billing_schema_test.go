package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Start from rows written without billing metadata, then run the production
// model migration twice. Existing quota/status must survive and the appended
// target must support amounts beyond a signed 32-bit integer.
func TestTaskBillingMetadataMigrationPreservesLegacyRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	type legacyTaskRow struct {
		ID     int64 `gorm:"primaryKey"`
		UserID int
		Quota  int64
		Status string
	}
	const largeQuota int64 = 1 << 33
	for _, table := range []string{"tasks", "midjourneys"} {
		require.NoError(t, db.Table(table).AutoMigrate(&legacyTaskRow{}))
		require.NoError(t, db.Table(table).Create(&legacyTaskRow{ID: 1, UserID: 7, Quota: largeQuota, Status: "IN_PROGRESS"}).Error)
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, db.AutoMigrate(&Task{}, &Midjourney{}))
	}
	var task Task
	var mj Midjourney
	require.NoError(t, db.First(&task, 1).Error)
	require.NoError(t, db.First(&mj, 1).Error)
	require.EqualValues(t, largeQuota, task.Quota)
	require.EqualValues(t, largeQuota, mj.Quota)
	require.EqualValues(t, "IN_PROGRESS", task.Status)
	require.Equal(t, "IN_PROGRESS", mj.Status)
	require.Empty(t, task.BillingState)
	require.Empty(t, mj.BillingState)
	require.Zero(t, task.BillingQuota)
	require.Zero(t, mj.BillingQuota)
	require.Zero(t, mj.TokenId)
	for _, value := range []interface{}{&task, &mj} {
		require.NoError(t, db.Model(value).UpdateColumn("billing_quota", largeQuota+1).Error)
		stmt := &gorm.Statement{DB: db}
		require.NoError(t, stmt.Parse(value))
		field := stmt.Schema.LookUpField("BillingQuota")
		require.NotNil(t, field)
		require.Equal(t, "bigint", string(field.DataType))
	}
	require.NoError(t, db.First(&task, 1).Error)
	require.NoError(t, db.First(&mj, 1).Error)
	require.EqualValues(t, largeQuota+1, task.BillingQuota)
	require.EqualValues(t, largeQuota+1, mj.BillingQuota)

	// The read-only integrity checker derives its expected columns from the
	// same models; appended billing metadata is recognized without exceptions.
	var report DatabaseIntegrityReport
	var issues []string
	inspectDBTarget(db, "task migration", []modelSpec{{Name: "Task", Model: &Task{}}, {Name: "Midjourney", Model: &Midjourney{}}}, &report,
		func(string) {}, func(issue string) { issues = append(issues, issue) }, func(string) {})
	require.Empty(t, issues)
	require.Equal(t, 2, report.CheckedTables)
}
