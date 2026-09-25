package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuotaLimitForColumnType(t *testing.T) {
	tests := []struct {
		name     string
		dialect  string
		database string
		column   string
		want     int64
	}{
		{name: "mysql int", dialect: "mysql", database: "int", column: "int", want: int64(^uint32(0) >> 1)},
		{name: "mysql bigint", dialect: "mysql", database: "bigint", column: "bigint", want: maxSignedStoredUserQuota},
		{name: "mysql unsigned int", dialect: "mysql", database: "int", column: "int unsigned", want: 4294967295},
		{name: "postgres integer", dialect: "postgres", database: "int4", column: "integer", want: int64(^uint32(0) >> 1)},
		{name: "postgres bigint", dialect: "postgres", database: "int8", column: "bigint", want: maxSignedStoredUserQuota},
		{name: "sqlite integer", dialect: "sqlite", database: "INTEGER", column: "INTEGER", want: maxStoredUserQuota},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := quotaLimitForColumnType(tt.dialect, tt.database, tt.column)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestQuotaLimitForColumnTypeRejectsUnknownType(t *testing.T) {
	_, err := quotaLimitForColumnType("mysql", "decimal", "decimal(20,0)")
	require.Error(t, err)
}
