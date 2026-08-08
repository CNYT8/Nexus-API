package model

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSanitizeDBErrorStripsDriverMessage(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"mysql", &mysql.MySQLError{Number: 1062, Message: "secret-value"}, "mysql error 1062"},
		{"postgres", &pgconn.PgError{Code: "23505", Message: "secret-value"}, "postgres error SQLSTATE 23505"},
		{"wrapped", fmt.Errorf("exec failed: %w", &mysql.MySQLError{Number: 1064, Message: "secret-value"}), "mysql error 1064"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, sanitizeDBError(test.err).Error())
		})
	}
}

func TestGormLoggerParameterizedOutsideDebug(t *testing.T) {
	previousDebug := common.DebugEnabled
	t.Cleanup(func() { common.DebugEnabled = previousDebug })
	common.DebugEnabled = false

	var output bytes.Buffer
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: newGormLogger(&output)})
	require.NoError(t, err)
	db.Exec("SELECT * FROM missing_table WHERE k = ?", "secret-value")

	assert.Contains(t, output.String(), "k = ?")
	assert.NotContains(t, output.String(), "secret-value")
	assert.Contains(t, output.String(), "sqlite error")
}
