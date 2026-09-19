package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupOAuthLegacyBindingControllerTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldUsingSQLite := common.UsingSQLite
	oldUsingMySQL := common.UsingMySQL
	oldUsingPostgreSQL := common.UsingPostgreSQL
	oldRedisEnabled := common.RedisEnabled
	oldRegisterEnabled := common.RegisterEnabled
	oldQuotaForNewUser := common.QuotaForNewUser

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.QuotaForNewUser = 0
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Log{},
		&model.TwoFA{},
		&model.TwoFABackupCode{},
		&model.PasskeyCredential{},
	))

	t.Cleanup(func() {
		common.RegisterEnabled = oldRegisterEnabled
		common.QuotaForNewUser = oldQuotaForNewUser
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.UsingSQLite = oldUsingSQLite
		common.UsingMySQL = oldUsingMySQL
		common.UsingPostgreSQL = oldUsingPostgreSQL
		common.RedisEnabled = oldRedisEnabled
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
}

// legacyBindingBaseProvider follows the GitHub provider contract: the numeric
// account ID identifies the user and Extra["legacy_id"] carries the stored login
// name. It deliberately does not implement VerifiedEmailProvider.
type legacyBindingBaseProvider struct {
	name               string
	providerUserID     string
	legacyID           string
	verifiedEmails     []string
	verifiedEmailsErr  error
	verifiedEmailCalls int
}

func (p *legacyBindingBaseProvider) GetName() string { return p.name }

func (p *legacyBindingBaseProvider) IsEnabled() bool { return true }

func (p *legacyBindingBaseProvider) ExchangeToken(context.Context, string, *gin.Context) (*oauth.OAuthToken, error) {
	return &oauth.OAuthToken{AccessToken: "legacy-binding-test-token"}, nil
}

func (p *legacyBindingBaseProvider) GetUserInfo(context.Context, *oauth.OAuthToken) (*oauth.OAuthUser, error) {
	return &oauth.OAuthUser{
		ProviderUserID: p.providerUserID,
		Username:       p.legacyID,
		DisplayName:    p.legacyID,
		Extra:          map[string]any{"legacy_id": p.legacyID},
	}, nil
}

func (p *legacyBindingBaseProvider) IsUserIDTaken(providerUserID string) bool {
	return model.IsGitHubIdAlreadyTaken(providerUserID)
}

func (p *legacyBindingBaseProvider) FillUserByProviderID(user *model.User, providerUserID string) error {
	user.GitHubId = providerUserID
	return user.FillUserByGitHubId()
}

func (p *legacyBindingBaseProvider) SetProviderUserID(user *model.User, providerUserID string) {
	user.GitHubId = providerUserID
}

func (p *legacyBindingBaseProvider) GetProviderPrefix() string { return "github_" }

type legacyBindingVerifiedEmailProvider struct {
	*legacyBindingBaseProvider
}

func (p *legacyBindingVerifiedEmailProvider) GetVerifiedEmails(context.Context, *oauth.OAuthToken) ([]string, error) {
	p.verifiedEmailCalls++
	return p.verifiedEmails, p.verifiedEmailsErr
}

// runFindOrCreateOAuthUserForTest invokes the OAuth login user resolution with a
// real cookie-backed session so affiliate and audit paths behave as in a request.
func runFindOrCreateOAuthUserForTest(t *testing.T, provider oauth.Provider, oauthUser *oauth.OAuthUser, token *oauth.OAuthToken) (*model.User, error) {
	t.Helper()
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("oauth-legacy-binding-controller-test"))))
	var result *model.User
	var callErr error
	router.GET("/run", func(c *gin.Context) {
		result, callErr = findOrCreateOAuthUser(c, provider, oauthUser, token, sessions.Default(c))
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/run", nil))
	return result, callErr
}

func TestFindOrCreateOAuthUserLegacyBindingRequiresEvidence(t *testing.T) {
	tests := []struct {
		name                   string
		legacyID               string
		existingGitHubID       string
		email                  string
		twoFA                  bool
		passkey                bool
		verifiedEmails         []string
		verifiedEmailsErr      error
		registerEnabled        bool
		providerVerifiedEmails bool
		wantRejected           bool
		wantMigrated           bool
		wantNewAccount         bool
		wantEmailCalls         int
		wantAudit              bool
		wantAuditSuccess       bool
		wantAuditReason        string
		wantAuditVerified      bool
	}{
		{
			name:                   "numeric legacy value registers a new account",
			legacyID:               "424242",
			existingGitHubID:       "424242",
			email:                  "legacy-github@example.com",
			verifiedEmails:         []string{"legacy-github@example.com"},
			registerEnabled:        true,
			providerVerifiedEmails: true,
			wantNewAccount:         true,
		},
		{
			name:                   "verified email match migrates",
			legacyID:               "octocat-legacy",
			existingGitHubID:       "octocat-legacy",
			email:                  "legacy-github@example.com",
			verifiedEmails:         []string{"other@example.com", " Legacy-GitHub@Example.com "},
			providerVerifiedEmails: true,
			wantMigrated:           true,
			wantEmailCalls:         1,
			wantAudit:              true,
			wantAuditSuccess:       true,
			wantAuditVerified:      true,
		},
		{
			name:                   "no matching verified email is rejected",
			legacyID:               "octocat-legacy",
			existingGitHubID:       "octocat-legacy",
			email:                  "legacy-github@example.com",
			verifiedEmails:         []string{"other@example.com"},
			registerEnabled:        true,
			providerVerifiedEmails: true,
			wantRejected:           true,
			wantEmailCalls:         1,
			wantAudit:              true,
			wantAuditReason:        "no_matching_evidence",
		},
		{
			name:                   "verified email fetch failure is rejected",
			legacyID:               "octocat-legacy",
			existingGitHubID:       "octocat-legacy",
			email:                  "legacy-github@example.com",
			verifiedEmailsErr:      errors.New("emails unavailable"),
			registerEnabled:        true,
			providerVerifiedEmails: true,
			wantRejected:           true,
			wantEmailCalls:         1,
			wantAudit:              true,
			wantAuditReason:        "verified_emails_unavailable",
		},
		{
			name:                   "account without email is rejected before asking the provider",
			legacyID:               "octocat-legacy",
			existingGitHubID:       "octocat-legacy",
			email:                  "",
			verifiedEmails:         []string{"legacy-github@example.com"},
			registerEnabled:        true,
			providerVerifiedEmails: true,
			wantRejected:           true,
			wantEmailCalls:         0,
			wantAudit:              true,
			wantAuditReason:        "no_matching_evidence",
		},
		{
			name:                   "provider without verified email support is rejected",
			legacyID:               "octocat-legacy",
			existingGitHubID:       "octocat-legacy",
			email:                  "legacy-github@example.com",
			registerEnabled:        true,
			providerVerifiedEmails: false,
			wantRejected:           true,
			wantAudit:              true,
			wantAuditReason:        "no_matching_evidence",
		},
		{
			name:                   "two factor account is rejected without asking the provider",
			legacyID:               "octocat-legacy",
			existingGitHubID:       "octocat-legacy",
			email:                  "legacy-github@example.com",
			twoFA:                  true,
			verifiedEmails:         []string{"legacy-github@example.com"},
			registerEnabled:        true,
			providerVerifiedEmails: true,
			wantRejected:           true,
			wantAudit:              true,
			wantAuditReason:        "two_factor_enabled",
		},
		{
			name:                   "passkey account is rejected without asking the provider",
			legacyID:               "octocat-legacy",
			existingGitHubID:       "octocat-legacy",
			email:                  "legacy-github@example.com",
			passkey:                true,
			verifiedEmails:         []string{"legacy-github@example.com"},
			registerEnabled:        true,
			providerVerifiedEmails: true,
			wantRejected:           true,
			wantAudit:              true,
			wantAuditReason:        "passkey_registered",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupOAuthLegacyBindingControllerTestDB(t)
			common.RegisterEnabled = test.registerEnabled

			existing := &model.User{
				Username: "legacy-github",
				Email:    test.email,
				Role:     common.RoleCommonUser,
				Status:   common.UserStatusEnabled,
				Group:    "default",
				AffCode:  "legacy-github-aff",
				GitHubId: test.existingGitHubID,
			}
			require.NoError(t, model.DB.Create(existing).Error)
			if test.twoFA {
				require.NoError(t, model.DB.Create(&model.TwoFA{UserId: existing.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}).Error)
			}
			if test.passkey {
				require.NoError(t, model.DB.Create(&model.PasskeyCredential{
					UserID:       existing.Id,
					CredentialID: "legacy-binding-passkey",
					PublicKey:    "legacy-binding-public-key",
				}).Error)
			}

			base := &legacyBindingBaseProvider{
				name:              "GitHub",
				providerUserID:    "900001",
				legacyID:          test.legacyID,
				verifiedEmails:    test.verifiedEmails,
				verifiedEmailsErr: test.verifiedEmailsErr,
			}
			var provider oauth.Provider = base
			if test.providerVerifiedEmails {
				provider = &legacyBindingVerifiedEmailProvider{legacyBindingBaseProvider: base}
			}
			oauthUser := &oauth.OAuthUser{
				ProviderUserID: "900001",
				Username:       test.legacyID,
				DisplayName:    test.legacyID,
				Email:          test.email,
				Extra:          map[string]any{"legacy_id": test.legacyID},
			}

			user, err := runFindOrCreateOAuthUserForTest(t, provider, oauthUser, &oauth.OAuthToken{AccessToken: "legacy-binding-test-token"})

			if test.wantRejected {
				require.Error(t, err)
				var notConfirmed *OAuthLegacyBindingNotConfirmedError
				assert.ErrorAs(t, err, &notConfirmed)
				assert.Nil(t, user)
			} else {
				require.NoError(t, err)
				require.NotNil(t, user)
			}
			assert.Equal(t, test.wantEmailCalls, base.verifiedEmailCalls)

			var reloaded model.User
			require.NoError(t, model.DB.Unscoped().First(&reloaded, existing.Id).Error)
			switch {
			case test.wantMigrated:
				assert.Equal(t, existing.Id, user.Id)
				assert.Equal(t, "900001", reloaded.GitHubId)
			case test.wantNewAccount:
				assert.NotEqual(t, existing.Id, user.Id)
				var created model.User
				require.NoError(t, model.DB.Where("github_id = ?", "900001").First(&created).Error)
				assert.Equal(t, created.Id, user.Id)
			default:
				assert.Equal(t, test.existingGitHubID, reloaded.GitHubId, "the existing binding must stay untouched")
			}

			var audits []model.Log
			require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeManage).Order("id").Find(&audits).Error)
			if !test.wantAudit {
				assert.Empty(t, audits)
				return
			}
			require.Len(t, audits, 1)
			assert.Equal(t, existing.Id, audits[0].UserId)
			var decoded struct {
				Op struct {
					Action string                 `json:"action"`
					Params map[string]interface{} `json:"params"`
				} `json:"op"`
			}
			require.NoError(t, common.UnmarshalJsonStr(audits[0].Other, &decoded))
			assert.Equal(t, "user.binding_bind", decoded.Op.Action)
			assert.Equal(t, "github", decoded.Op.Params["provider"])
			assert.Equal(t, test.legacyID, decoded.Op.Params["legacy_id"])
			assert.Equal(t, "900001", decoded.Op.Params["provider_user_id"])
			assert.Equal(t, test.wantAuditSuccess, decoded.Op.Params["success"])
			if test.wantAuditReason != "" {
				assert.Equal(t, test.wantAuditReason, decoded.Op.Params["reason"])
			}
			if test.wantAuditVerified {
				assert.Equal(t, true, decoded.Op.Params["verified_email_matched"])
			}
		})
	}
}
