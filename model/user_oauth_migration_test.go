package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigrateLegacyGitHubBindingWithTx(t *testing.T) {
	tests := []struct {
		name          string
		initialID     string
		legacyID      string
		newID         string
		otherGitHubID string
		wantWritten   bool
		wantErr       error
		wantGitHubID  string
	}{
		{
			name:         "compare and swap succeeds",
			initialID:    "octocat-legacy",
			legacyID:     "octocat-legacy",
			newID:        "900001",
			wantWritten:  true,
			wantGitHubID: "900001",
		},
		{
			name:         "legacy binding changed meanwhile is left untouched",
			initialID:    "relinked-legacy",
			legacyID:     "octocat-legacy",
			newID:        "900001",
			wantWritten:  false,
			wantGitHubID: "relinked-legacy",
		},
		{
			name:          "numeric id claimed by another account is rejected",
			initialID:     "octocat-legacy",
			legacyID:      "octocat-legacy",
			newID:         "900001",
			otherGitHubID: "900001",
			wantErr:       ErrGitHubBindingAlreadyClaimed,
			wantGitHubID:  "octocat-legacy",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)

			user := &User{Username: "legacy-migration-user", GitHubId: test.initialID, Status: 1, Role: 1, AffCode: "legacy-migration-user-aff"}
			require.NoError(t, DB.Create(user).Error)
			other := &User{Username: "legacy-migration-other", GitHubId: test.otherGitHubID, Status: 1, Role: 1, AffCode: "legacy-migration-other-aff"}
			if test.otherGitHubID == "" {
				other.GitHubId = "unrelated-binding"
			}
			require.NoError(t, DB.Create(other).Error)

			var written bool
			err := DB.Transaction(func(tx *gorm.DB) error {
				var txErr error
				written, txErr = MigrateLegacyGitHubBindingWithTx(tx, user.Id, test.legacyID, test.newID)
				return txErr
			})
			if test.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, test.wantWritten, written)

			var reloaded User
			require.NoError(t, DB.First(&reloaded, user.Id).Error)
			assert.Equal(t, test.wantGitHubID, reloaded.GitHubId)

			// A migration must never rewrite the binding of another account.
			var reloadedOther User
			require.NoError(t, DB.First(&reloadedOther, other.Id).Error)
			assert.Equal(t, other.GitHubId, reloadedOther.GitHubId)
		})
	}
}

func TestMigrateLegacyGitHubBindingWithTxRejectsInvalidInput(t *testing.T) {
	truncateTables(t)
	user := &User{Username: "legacy-invalid-input", GitHubId: "octocat-legacy", Status: 1, Role: 1, AffCode: "legacy-invalid-input-aff"}
	require.NoError(t, DB.Create(user).Error)

	for _, test := range []struct {
		name     string
		userID   int
		legacyID string
		newID    string
	}{
		{name: "zero user id", userID: 0, legacyID: "octocat-legacy", newID: "900001"},
		{name: "empty legacy id", userID: user.Id, legacyID: "", newID: "900001"},
		{name: "empty github id", userID: user.Id, legacyID: "octocat-legacy", newID: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			written, err := MigrateLegacyGitHubBindingWithTx(DB, test.userID, test.legacyID, test.newID)
			require.Error(t, err)
			assert.False(t, written)
			assert.False(t, errors.Is(err, ErrGitHubBindingAlreadyClaimed))
		})
	}
}
