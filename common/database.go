package common

import "strings"

const (
	DatabaseTypeMySQL      = "mysql"
	DatabaseTypeSQLite     = "sqlite"
	DatabaseTypePostgreSQL = "postgres"
	DatabaseTypeClickHouse = "clickhouse"
)

var UsingSQLite = false
var UsingPostgreSQL = false
var LogSqlType = DatabaseTypeSQLite // Default to SQLite for logging SQL queries
var UsingMySQL = false

// UsingClickHouse describes LOG_DB only; the primary DB stays relational.
var UsingClickHouse = false

// SQLitePath is the DSN for the default SQLite database. It uses WAL journal
// mode so readers are never blocked by the single writer, plus a 30s busy
// timeout for writers to queue.
//
// Two details are non-obvious and both are required for concurrent correctness:
//
//  1. The busy timeout must be passed as a `_pragma=busy_timeout(30000)` DSN
//     parameter. The pure-Go driver (modernc.org/sqlite, used through
//     github.com/glebarez/sqlite) silently ignores the plain `_busy_timeout=`
//     form, so without this the effective timeout stays at SQLite's 5s default
//     and concurrent writes surface as "database is locked".
//
//  2. `_txlock=immediate` (BEGIN IMMEDIATE) must be enabled. Without it, a
//     transaction that first SELECTs (establishing a read snapshot) and then
//     writes can hit SQLITE_BUSY_SNAPSHOT when another connection commits in
//     between; the busy handler does not cover that case, so the write fails
//     instantly no matter the timeout. BEGIN IMMEDIATE takes the write lock up
//     front, so writers serialize through the busy timeout instead of dying on
//     a stale snapshot. Autocommit SELECTs stay concurrent because WAL keeps
//     readers unlocked.
const sqliteConcurrencyParams = "_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate"

var SQLitePath = "one-api.db?" + sqliteConcurrencyParams

// sqliteDSN turns a SQLITE_PATH value into a DSN. A bare path gets the
// default concurrency parameters; a value with its own query string is used
// as given.
func sqliteDSN(path string) string {
	if strings.Contains(path, "?") {
		return path
	}
	return path + "?" + sqliteConcurrencyParams
}
