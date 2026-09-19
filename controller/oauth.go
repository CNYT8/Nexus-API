package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// providerParams returns map with Provider key for i18n templates
func providerParams(name string) map[string]any {
	return map[string]any{"Provider": name}
}

// GenerateOAuthCode generates a state code for OAuth CSRF protection
func GenerateOAuthCode(c *gin.Context) {
	session := sessions.Default(c)
	state := common.GetRandomString(12)
	affCode := c.Query("aff")
	if affCode != "" {
		session.Set("aff", affCode)
	}
	session.Set("oauth_state", state)
	err := session.Save()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    state,
	})
}

// HandleOAuth handles OAuth callback for all standard OAuth providers
func HandleOAuth(c *gin.Context) {
	providerName := c.Param("provider")
	provider := oauth.GetProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgOAuthUnknownProvider),
		})
		return
	}

	session := sessions.Default(c)

	// 1. Validate state (CSRF protection)
	state := c.Query("state")
	if state == "" || session.Get("oauth_state") == nil || state != session.Get("oauth_state").(string) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgOAuthStateInvalid),
		})
		return
	}

	// 2. Check if user is already logged in (bind flow)
	username := session.Get("username")
	if username != nil {
		handleOAuthBind(c, provider)
		return
	}

	// 3. Check if provider is enabled
	if !provider.IsEnabled() {
		common.ApiErrorI18n(c, i18n.MsgOAuthNotEnabled, providerParams(provider.GetName()))
		return
	}

	// 4. Handle error from provider
	errorCode := c.Query("error")
	if errorCode != "" {
		errorDescription := c.Query("error_description")
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": errorDescription,
		})
		return
	}

	// 5. Exchange code for token
	code := c.Query("code")
	token, err := provider.ExchangeToken(c.Request.Context(), code, c)
	if err != nil {
		handleOAuthError(c, err)
		return
	}

	// 6. Get user info
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		handleOAuthError(c, err)
		return
	}

	// 7. Find or create user
	user, err := findOrCreateOAuthUser(c, provider, oauthUser, token, session)
	if err != nil {
		switch err.(type) {
		case *OAuthUserDeletedError:
			common.ApiErrorI18n(c, i18n.MsgOAuthUserDeleted)
		case *OAuthRegistrationDisabledError:
			common.ApiErrorI18n(c, i18n.MsgUserRegisterDisabled)
		case *OAuthLegacyBindingNotConfirmedError:
			common.ApiErrorI18n(c, i18n.MsgOAuthNotAutoLinked, providerParams(provider.GetName()))
		default:
			common.ApiError(c, err)
		}
		return
	}

	// 8. Check user status
	if user.Status != common.UserStatusEnabled {
		common.ApiErrorI18n(c, i18n.MsgOAuthUserBanned)
		return
	}

	// 9. Setup login
	setupLogin(user, c)
}

// handleOAuthBind handles binding OAuth account to existing user
func handleOAuthBind(c *gin.Context, provider oauth.Provider) {
	if !provider.IsEnabled() {
		common.ApiErrorI18n(c, i18n.MsgOAuthNotEnabled, providerParams(provider.GetName()))
		return
	}

	// Exchange code for token
	code := c.Query("code")
	token, err := provider.ExchangeToken(c.Request.Context(), code, c)
	if err != nil {
		handleOAuthError(c, err)
		return
	}

	// Get user info
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		handleOAuthError(c, err)
		return
	}

	// Check if this OAuth account is already bound
	if provider.IsUserIDTaken(oauthUser.ProviderUserID) {
		common.ApiErrorI18n(c, i18n.MsgOAuthAlreadyBound, providerParams(provider.GetName()))
		return
	}

	// Get current user from session
	session := sessions.Default(c)
	id := session.Get("id")
	user := model.User{Id: id.(int)}
	err = user.FillUserById()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// Handle binding based on provider type
	if genericProvider, ok := provider.(*oauth.GenericOAuthProvider); ok {
		// Custom provider: use user_oauth_bindings table
		err = model.UpdateUserOAuthBinding(user.Id, genericProvider.GetProviderId(), oauthUser.ProviderUserID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	} else {
		// Built-in provider: update user record directly
		provider.SetProviderUserID(&user, oauthUser.ProviderUserID)
		err = user.Update(false)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}

	common.ApiSuccessI18n(c, i18n.MsgOAuthBindSuccess, gin.H{
		"action": "bind",
	})
}

// findOrCreateOAuthUser finds the existing user or creates a new one. A legacy
// GitHub binding (a login name stored before numeric IDs were used) only points
// at a candidate account and must be confirmed before that account logs in.
func findOrCreateOAuthUser(c *gin.Context, provider oauth.Provider, oauthUser *oauth.OAuthUser, token *oauth.OAuthToken, session sessions.Session) (*model.User, error) {
	user := &model.User{}

	// Check if user already exists with new ID
	if provider.IsUserIDTaken(oauthUser.ProviderUserID) {
		err := provider.FillUserByProviderID(user, oauthUser.ProviderUserID)
		if err != nil {
			return nil, err
		}
		// Check if user has been deleted
		if user.Id == 0 {
			return nil, &OAuthUserDeletedError{}
		}
		return user, nil
	}

	// Legacy GitHub bindings stored the login name, which only points at a
	// candidate account. Values that are all digits are already numeric account
	// IDs and never take part in the comparison.
	legacyID, _ := oauthUser.Extra["legacy_id"].(string)
	if strings.EqualFold(provider.GetName(), "GitHub") && containsNonDigit(legacyID) && provider.IsUserIDTaken(legacyID) {
		if err := provider.FillUserByProviderID(user, legacyID); err != nil {
			return nil, err
		}
		if user.Id != 0 {
			if err := migrateLegacyOAuthBinding(c, provider, user, legacyID, oauthUser.ProviderUserID, token); err != nil {
				return nil, err
			}
			return user, nil
		}
	}

	// User doesn't exist, create new user if registration is enabled
	if !common.RegisterEnabled {
		return nil, &OAuthRegistrationDisabledError{}
	}

	// Set up new user
	user.Username = provider.GetProviderPrefix() + strconv.Itoa(model.GetMaxUserId()+1)

	if oauthUser.Username != "" {
		if exists, err := model.CheckUserExistOrDeleted(oauthUser.Username, ""); err == nil && !exists {
			// 防止索引退化
			if len(oauthUser.Username) <= model.UserNameMaxLength {
				user.Username = oauthUser.Username
			}
		}
	}

	if oauthUser.DisplayName != "" {
		user.DisplayName = oauthUser.DisplayName
	} else if oauthUser.Username != "" {
		user.DisplayName = oauthUser.Username
	} else {
		user.DisplayName = provider.GetName() + " User"
	}
	if oauthUser.Email != "" {
		user.Email = oauthUser.Email
	}
	user.Role = common.RoleCommonUser
	user.Status = common.UserStatusEnabled
	user.RegisterIp = c.ClientIP()

	// Handle affiliate code
	affCode := session.Get("aff")
	inviterId := 0
	if affCode != nil {
		inviterId, _ = model.GetUserIdByAffCode(affCode.(string))
	}

	// Use transaction to ensure user creation and OAuth binding are atomic
	if genericProvider, ok := provider.(*oauth.GenericOAuthProvider); ok {
		// Custom provider: create user and binding in a transaction
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Create user
			if err := user.InsertWithTx(tx, inviterId); err != nil {
				return err
			}

			// Create OAuth binding
			binding := &model.UserOAuthBinding{
				UserId:         user.Id,
				ProviderId:     genericProvider.GetProviderId(),
				ProviderUserId: oauthUser.ProviderUserID,
			}
			if err := model.CreateUserOAuthBindingWithTx(tx, binding); err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		// Perform post-transaction tasks (logs, sidebar config, inviter rewards)
		user.FinalizeOAuthUserCreation(inviterId)
	} else {
		// Built-in provider: create user and update provider ID in a transaction
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Create user
			if err := user.InsertWithTx(tx, inviterId); err != nil {
				return err
			}

			// Set the provider user ID on the user model and update
			provider.SetProviderUserID(user, oauthUser.ProviderUserID)
			if err := tx.Model(user).Updates(map[string]interface{}{
				"github_id":   user.GitHubId,
				"discord_id":  user.DiscordId,
				"oidc_id":     user.OidcId,
				"linux_do_id": user.LinuxDOId,
				"wechat_id":   user.WeChatId,
				"telegram_id": user.TelegramId,
			}).Error; err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		// Perform post-transaction tasks
		user.FinalizeOAuthUserCreation(inviterId)
	}

	return user, nil
}

// containsNonDigit reports whether value holds at least one character that is
// not an ASCII digit. Empty values and all-digit values therefore never identify
// a stored legacy login name.
func containsNonDigit(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' })
}

// migrateLegacyOAuthBinding confirms that a legacy GitHub binding belongs to the
// signed-in GitHub account before rewriting it to the numeric account ID. A
// second factor or passkey would normally re-prove ownership during login, but
// Nexus has no such login verification chain, so those accounts are rejected
// instead of migrating on provider evidence alone. Every decision is recorded
// as a user.binding_bind audit event.
func migrateLegacyOAuthBinding(c *gin.Context, provider oauth.Provider, user *model.User, legacyID, providerUserID string, token *oauth.OAuthToken) error {
	reject := func(reason string) error {
		recordLegacyOAuthBindingAudit(c, user, false, map[string]interface{}{
			"legacy_id":        legacyID,
			"provider_user_id": providerUserID,
			"reason":           reason,
		})
		return &OAuthLegacyBindingNotConfirmedError{}
	}

	twoFAEnabled, err := model.IsTwoFAEnabled(user.Id)
	if err != nil {
		common.SysError(fmt.Sprintf("[OAuth] Failed to read 2FA state for user %d: %s", user.Id, err.Error()))
		return reject("verification_state_unavailable")
	}
	if twoFAEnabled {
		return reject("two_factor_enabled")
	}
	if _, err := model.GetPasskeyByUserID(user.Id); err == nil {
		return reject("passkey_registered")
	} else if !errors.Is(err, model.ErrPasskeyNotFound) {
		common.SysError(fmt.Sprintf("[OAuth] Failed to read passkey state for user %d: %s", user.Id, err.Error()))
		return reject("verification_state_unavailable")
	}

	// Without a second factor, one of the addresses the provider has confirmed
	// must match the account email. The list is fetched only here and never
	// recorded.
	accountEmail := normalizeOAuthEmail(user.Email)
	emailProvider, ok := provider.(oauth.VerifiedEmailProvider)
	if accountEmail == "" || !ok {
		return reject("no_matching_evidence")
	}
	emails, err := emailProvider.GetVerifiedEmails(c.Request.Context(), token)
	if err != nil {
		common.SysError(fmt.Sprintf("[OAuth] Failed to load verified emails for user %d", user.Id))
		return reject("verified_emails_unavailable")
	}
	matched := false
	for _, email := range emails {
		if normalizeOAuthEmail(email) == accountEmail {
			matched = true
			break
		}
	}
	if !matched {
		return reject("no_matching_evidence")
	}

	written := false
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		// Bind the email evidence to the same account snapshot as the CAS. A
		// changed/disabled account cannot inherit an earlier lookup's evidence.
		var current model.User
		query := tx.Select("id", "email", "status")
		if tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&current, user.Id).Error; err != nil {
			return err
		}
		if normalizeOAuthEmail(current.Email) != accountEmail || current.Status != common.UserStatusEnabled {
			return nil
		}
		var txErr error
		written, txErr = model.MigrateLegacyGitHubBindingWithTx(tx, user.Id, legacyID, providerUserID)
		return txErr
	})
	if err != nil {
		if errors.Is(err, model.ErrGitHubBindingAlreadyClaimed) {
			return reject("provider_user_id_claimed")
		}
		common.SysError(fmt.Sprintf("[OAuth] Failed to migrate legacy GitHub binding for user %d: %s", user.Id, err.Error()))
		return reject("migration_failed")
	}
	if !written {
		// The binding changed between the candidate lookup and the transaction;
		// the stale login-name evidence no longer owns this account.
		return reject("binding_changed")
	}

	user.GitHubId = providerUserID
	recordLegacyOAuthBindingAudit(c, user, true, map[string]interface{}{
		"legacy_id":              legacyID,
		"provider_user_id":       providerUserID,
		"verified_email_matched": true,
	})
	return nil
}

// normalizeOAuthEmail lowercases and trims an address so provider and account
// spellings compare consistently.
func normalizeOAuthEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// recordLegacyOAuthBindingAudit records the outcome of a legacy GitHub binding
// rewrite as an account binding event. No session exists yet on the login path,
// so the row carries the user's own role instead of a request operator.
func recordLegacyOAuthBindingAudit(c *gin.Context, user *model.User, success bool, params map[string]interface{}) {
	params["provider"] = "github"
	params["legacy_migration"] = true
	params["success"] = success
	model.RecordOperationAuditLog(user.Id, auditContentEN("user.binding_bind", params), c.ClientIP(), "user.binding_bind", params, nil, nil)
}

type OAuthUserDeletedError struct{}

func (e *OAuthUserDeletedError) Error() string {
	return "user has been deleted"
}

type OAuthRegistrationDisabledError struct{}

func (e *OAuthRegistrationDisabledError) Error() string {
	return "registration is disabled"
}

// OAuthLegacyBindingNotConfirmedError reports a legacy GitHub binding match that
// neither a second factor nor a confirmed provider email backed.
type OAuthLegacyBindingNotConfirmedError struct{}

func (e *OAuthLegacyBindingNotConfirmedError) Error() string {
	return "legacy binding was not confirmed"
}

// handleOAuthError handles OAuth errors and returns translated message
func handleOAuthError(c *gin.Context, err error) {
	switch e := err.(type) {
	case *oauth.OAuthError:
		if e.Params != nil {
			common.ApiErrorI18n(c, e.MsgKey, e.Params)
		} else {
			common.ApiErrorI18n(c, e.MsgKey)
		}
	case *oauth.AccessDeniedError:
		common.ApiErrorMsg(c, e.Message)
	case *oauth.TrustLevelError:
		common.ApiErrorI18n(c, i18n.MsgOAuthTrustLevelLow)
	default:
		common.ApiError(c, err)
	}
}
