package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const DashboardAccessTokenPrefix = "npat_"

var (
	ErrAccessTokenLimit   = errors.New("personal access token limit reached")
	ErrAccessTokenInvalid = errors.New("invalid personal access token update")
)

type UserAccessToken struct {
	Id          int64  `json:"id" gorm:"primaryKey"`
	UserId      int    `json:"-" gorm:"index;not null"`
	Name        string `json:"name" gorm:"type:varchar(100)"`
	SecretHash  string `json:"-" gorm:"type:char(64);uniqueIndex;not null"`
	Prefix      string `json:"prefix" gorm:"type:varchar(20)"`
	Scopes      string `json:"scopes" gorm:"type:text"`
	AuthVersion int64  `json:"-"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at" gorm:"index"`
	RevokedAt   int64  `json:"revoked_at"`
}

func NormalizeAccessTokenScopes(scopes []string, role int) ([]string, error) {
	if len(scopes) == 0 || len(scopes) > 32 {
		return nil, errors.New("access token scopes are required")
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		parts := strings.Split(scope, ":")
		if len(parts) != 2 || (parts[1] != "read" && parts[1] != "write" && scope != "token:reveal") {
			return nil, errors.New("invalid access token scope")
		}
		switch parts[0] {
		case "self", "token", "task":
		case AdminPermissionChannel, AdminPermissionModels, AdminPermissionUser, AdminPermissionRedemption, AdminPermissionSubscription, AdminPermissionTicket:
			if role < common.RoleAdminUser {
				return nil, errors.New("insufficient role for access token scope")
			}
		case "system":
			if role < common.RoleRootUser {
				return nil, errors.New("insufficient role for system scope")
			}
		default:
			return nil, errors.New("unknown access token scope")
		}
		if !seen[scope] {
			seen[scope] = true
			result = append(result, scope)
		}
	}
	return result, nil
}

func (token *UserAccessToken) Allows(module, action string) bool {
	var scopes []string
	if token == nil || json.Unmarshal([]byte(token.Scopes), &scopes) != nil {
		return false
	}
	for _, scope := range scopes {
		if scope == module+":"+action {
			return true
		}
	}
	return false
}

// AccessTokenAllows adds a PAT scope check after the normal authentication and
// account/module permission checks. Root PATs have no scope bypass. A typed nil
// token denotes an authenticated legacy credential; sessions have no token key.
func AccessTokenAllows(c *gin.Context, module, action string) bool {
	value, exists := c.Get("access_token")
	if !exists {
		return !c.GetBool("use_access_token")
	}
	token, ok := value.(*UserAccessToken)
	if !ok {
		return false
	}
	return (token == nil && action != "reveal") || token.Allows(module, action)
}

func CreateDashboardAccessToken(user *User, name string, scopes []string, expiresAt int64) (string, *UserAccessToken, error) {
	return createDashboardAccessToken(user, name, scopes, expiresAt, false)
}

func RotateDashboardAccessToken(user *User, scopes []string, expiresAt int64) (string, *UserAccessToken, error) {
	return createDashboardAccessToken(user, "dashboard", scopes, expiresAt, true)
}

func createDashboardAccessToken(user *User, name string, scopes []string, expiresAt int64, rotate bool) (string, *UserAccessToken, error) {
	now := time.Now().Unix()
	if user == nil || user.Id <= 0 || user.Status != common.UserStatusEnabled || len(name) > 100 || expiresAt <= now || expiresAt > now+int64(365*24*time.Hour/time.Second) {
		return "", nil, ErrAuthStateInvalid
	}
	scopes, err := NormalizeAccessTokenScopes(scopes, user.Role)
	if err != nil {
		return "", nil, err
	}
	secret, err := NewAuthSecret()
	if err != nil {
		return "", nil, err
	}
	secret = DashboardAccessTokenPrefix + secret
	encoded, err := json.Marshal(scopes)
	if err != nil {
		return "", nil, err
	}
	record := &UserAccessToken{UserId: user.Id, Name: name, SecretHash: authSecretHash("pat", secret), Prefix: secret[:12],
		Scopes: string(encoded), AuthVersion: user.AuthVersion, CreatedAt: now, ExpiresAt: expiresAt}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
			return err
		}
		var current User
		if err := tx.First(&current, user.Id).Error; err != nil {
			return err
		}
		if current.AuthVersion != user.AuthVersion || current.Status != common.UserStatusEnabled {
			return ErrAuthStateInvalid
		}
		if _, err := NormalizeAccessTokenScopes(scopes, current.Role); err != nil {
			return err
		}
		now = time.Now().Unix()
		if expiresAt <= now {
			return ErrAuthStateInvalid
		}
		record.CreatedAt = now
		if rotate {
			if err := tx.Model(&UserAccessToken{}).Where("user_id = ? AND name = ? AND revoked_at = 0", user.Id, "dashboard").Update("revoked_at", now).Error; err != nil {
				return err
			}
			if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("access_token", nil).Error; err != nil {
				return err
			}
		}
		// The owner's first no-op UPDATE serializes count-and-insert on all
		// three dialects. Historical tombstones never consume an active slot.
		var count int64
		if err := tx.Model(&UserAccessToken{}).Where("user_id = ? AND auth_version = ? AND revoked_at = 0 AND expires_at > ?",
			user.Id, current.AuthVersion, now).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(operation_setting.GetAuthenticationSetting().MaxAccessTokens) {
			return ErrAccessTokenLimit
		}
		return tx.Create(record).Error
	})
	if err != nil {
		return "", nil, err
	}
	return secret, record, nil
}

// Legacy plaintext tokens remain readable until explicitly rotated/revoked or
// the account security version is changed (which also clears access_token).
func ValidateDashboardAccessToken(raw string) (*User, *UserAccessToken, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 7 && strings.EqualFold(raw[:7], "Bearer ") {
		raw = strings.TrimSpace(raw[7:])
	}
	if raw == "" {
		return nil, nil, nil
	}
	var user User
	if !strings.HasPrefix(raw, DashboardAccessTokenPrefix) {
		// Upstream PAT/JWT formats are not Nexus credentials. In an upstream
		// cutover even legacy plaintext-token retirement cannot be inferred,
		// so require explicit reissuance rather than broadening authorization.
		if strings.HasPrefix(raw, "nap_") {
			return nil, nil, nil
		}
		allowed, err := allowLegacySessionAdoption(DB)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrDatabase, err)
		}
		if !allowed {
			return nil, nil, nil
		}
		err = DB.Where("access_token = ?", raw).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrDatabase, err)
		}
		return &user, nil, nil
	}
	var token UserAccessToken
	err := DB.Where("secret_hash = ? AND revoked_at = 0 AND expires_at > ?", authSecretHash("pat", raw), time.Now().Unix()).First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	if err := DB.First(&user, token.UserId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	if user.AuthVersion != token.AuthVersion || user.Status != common.UserStatusEnabled {
		return nil, nil, nil
	}
	return &user, &token, nil
}

func ListDashboardAccessTokens(userID int) ([]UserAccessToken, error) {
	var tokens []UserAccessToken
	// Active tokens must never be hidden behind accumulated revoked rows.
	limit := operation_setting.GetAuthenticationSetting().MaxAccessTokens
	err := DB.Where("user_id = ?", userID).
		Order("CASE WHEN revoked_at = 0 THEN 0 ELSE 1 END").
		Order("id DESC").
		Limit(limit).
		Find(&tokens).Error
	return tokens, err
}

type DashboardAccessTokenItem struct {
	UserAccessToken
	Status string `json:"status"`
}

type DashboardAccessTokenPage struct {
	Page        int                        `json:"page"`
	PageSize    int                        `json:"page_size"`
	Total       int64                      `json:"total"`
	ActiveCount int64                      `json:"active_count"`
	Limit       int                        `json:"limit"`
	Items       []DashboardAccessTokenItem `json:"items"`
}

func dashboardAccessTokenStatus(token UserAccessToken, version, now int64) string {
	switch {
	case token.RevokedAt != 0:
		return "revoked"
	case token.AuthVersion != version:
		return "invalidated"
	case token.ExpiresAt <= now:
		return "expired"
	default:
		return "active"
	}
}

func ListDashboardAccessTokensPage(userID, page, pageSize int) (*DashboardAccessTokenPage, error) {
	if userID <= 0 || page < 1 || pageSize < 1 || pageSize > 100 || page > int(^uint(0)>>1)/pageSize {
		return nil, ErrAccessTokenInvalid
	}
	var user User
	if err := DB.Select("id", "auth_version", "status").First(&user, userID).Error; err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled {
		return nil, ErrAuthStateInvalid
	}
	result := &DashboardAccessTokenPage{Page: page, PageSize: pageSize,
		Limit: operation_setting.GetAuthenticationSetting().MaxAccessTokens, Items: []DashboardAccessTokenItem{}}
	if err := DB.Model(&UserAccessToken{}).Where("user_id = ?", userID).Count(&result.Total).Error; err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if err := DB.Model(&UserAccessToken{}).Where("user_id = ? AND auth_version = ? AND revoked_at = 0 AND expires_at > ?",
		userID, user.AuthVersion, now).Count(&result.ActiveCount).Error; err != nil {
		return nil, err
	}
	var tokens []UserAccessToken
	// Active tokens sort first so revoked history cannot push them past the page.
	if err := DB.Where("user_id = ?", userID).
		Order("CASE WHEN revoked_at = 0 THEN 0 ELSE 1 END").
		Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&tokens).Error; err != nil {
		return nil, err
	}
	for _, token := range tokens {
		result.Items = append(result.Items, DashboardAccessTokenItem{UserAccessToken: token,
			Status: dashboardAccessTokenStatus(token, user.AuthVersion, now)})
	}
	return result, nil
}

// UpdateDashboardAccessToken changes only explicit fields under the owner's
// lock. The secret, auth version, expiry, issuance time and tombstone are immutable.
func UpdateDashboardAccessToken(user *User, tokenID int64, name *string, scopes []string) (*UserAccessToken, error) {
	if user == nil || user.Id <= 0 || tokenID <= 0 || (name == nil && scopes == nil) || (name != nil && len(*name) > 100) {
		return nil, ErrAccessTokenInvalid
	}
	var record UserAccessToken
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
			return err
		}
		var current User
		if err := tx.First(&current, user.Id).Error; err != nil {
			return err
		}
		if current.Status != common.UserStatusEnabled || current.AuthVersion != user.AuthVersion {
			return ErrAuthStateInvalid
		}
		if err := tx.Where("id = ? AND user_id = ? AND auth_version = ? AND revoked_at = 0 AND expires_at > ?",
			tokenID, user.Id, current.AuthVersion, time.Now().Unix()).First(&record).Error; err != nil {
			return err
		}
		updates := map[string]any{}
		if name != nil {
			record.Name = *name
			updates["name"] = *name
		}
		if scopes != nil {
			normalized, err := NormalizeAccessTokenScopes(scopes, current.Role)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrAccessTokenInvalid, err)
			}
			encoded, err := json.Marshal(normalized)
			if err != nil {
				return err
			}
			record.Scopes = string(encoded)
			updates["scopes"] = record.Scopes
		}
		return tx.Model(&UserAccessToken{}).Where("id = ? AND user_id = ?", record.Id, user.Id).Updates(updates).Error
	})
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func RevokeDashboardAccessToken(userID int, tokenID int64) error {
	result := DB.Model(&UserAccessToken{}).Where("id = ? AND user_id = ?", tokenID, userID).Update("revoked_at", time.Now().Unix())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
