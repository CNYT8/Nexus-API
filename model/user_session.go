package model

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAuthStateInvalid = errors.New("authentication state is invalid or expired")

var (
	ErrSessionActiveLimit   = errors.New("active session limit reached")
	ErrSessionIssuanceLimit = errors.New("session issuance window limit reached")
)

type UserSessionLimits struct {
	ActiveCount   int64 `json:"active_count"`
	ActiveLimit   int   `json:"active_limit"`
	IssuanceCount int64 `json:"issuance_count"`
	IssuanceLimit int   `json:"issuance_limit"`
	WindowSeconds int64 `json:"window_seconds"`
}

func readUserSessionLimits(db *gorm.DB, user *User, now int64) (*UserSessionLimits, error) {
	limits := operation_setting.GetAuthenticationSetting()
	result := &UserSessionLimits{ActiveLimit: limits.MaxActiveSessions,
		IssuanceLimit: limits.MaxSessionIssuance, WindowSeconds: limits.SessionWindowSeconds}
	if err := db.Model(&UserSession{}).Where("user_id = ? AND auth_version = ? AND revoked_at = 0 AND expires_at > ?",
		user.Id, user.AuthVersion, now).Count(&result.ActiveCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&UserSession{}).Where("user_id = ? AND created_at >= ?",
		user.Id, now-limits.SessionWindowSeconds).Count(&result.IssuanceCount).Error; err != nil {
		return nil, err
	}
	return result, nil
}

// GetUserSessionLimits returns display counts only after validating the owned
// current session. Issuance always checks again under the owner's write lock.
func GetUserSessionLimits(userID int, currentID int64) (*UserSessionLimits, error) {
	user, _, err := managedSessionIdentity(DB, userID, currentID)
	if err != nil {
		return nil, err
	}
	return readUserSessionLimits(DB, user, time.Now().Unix())
}

// checkUserSessionLimits runs only after acquiring the owner's write lock.
// Count every issuance in the window, including revoked, expired and invalidated
// sessions; otherwise a logout or password reset would reset the rate limit.
func checkUserSessionLimits(tx *gorm.DB, user *User, now int64) error {
	limits, err := readUserSessionLimits(tx, user, now)
	if err != nil {
		return err
	}
	if limits.ActiveCount >= int64(limits.ActiveLimit) {
		return ErrSessionActiveLimit
	}
	if limits.IssuanceCount >= int64(limits.IssuanceLimit) {
		return ErrSessionIssuanceLimit
	}
	return nil
}

const UserSessionLifetime = 30 * 24 * time.Hour

// Only the random session secret reaches the signed cookie. Its hash is stored here.
type UserSession struct {
	Id             int64  `json:"id" gorm:"primaryKey"`
	UserId         int    `json:"-" gorm:"index;not null"`
	SecretHash     string `json:"-" gorm:"type:char(64);uniqueIndex;not null"`
	AuthVersion    int64  `json:"-" gorm:"not null"`
	CreatedAt      int64  `json:"created_at"`
	ExpiresAt      int64  `json:"expires_at" gorm:"index"`
	RevokedAt      int64  `json:"revoked_at"`
	VerifiedAt     int64  `json:"-"`
	VerifiedMethod string `json:"-" gorm:"type:varchar(32)"`
	LoginMethod    string `json:"login_method" gorm:"type:varchar(64)"`
	IP             string `json:"ip" gorm:"type:varchar(64)"`
	UserAgent      string `json:"user_agent" gorm:"type:varchar(512)"`
}

func NewAuthSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func authSecretHash(domain, secret string) string {
	return common.GenerateHMACWithKey([]byte("nexus-auth:"+domain+":"+common.SessionSecret), secret)
}

func CreateUserSession(user *User, method, ip, agent string) (string, error) {
	if user == nil || user.Id <= 0 || user.Status != common.UserStatusEnabled {
		return "", ErrAuthStateInvalid
	}
	secret, err := NewAuthSecret()
	if err != nil {
		return "", err
	}
	if len(agent) > 512 {
		agent = agent[:512]
	}
	now := time.Now().Unix()
	record := UserSession{UserId: user.Id, SecretHash: authSecretHash("session", secret), AuthVersion: user.AuthVersion,
		CreatedAt: now, ExpiresAt: now + int64(UserSessionLifetime/time.Second), LoginMethod: method, IP: ip, UserAgent: agent}
	if method == "password" || method == "2fa" || method == "passkey" {
		record.VerifiedAt, record.VerifiedMethod = now, method
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		// First write serializes session creation with password changes on all dialects.
		result := tx.Model(&User{}).Where("id = ? AND auth_version = ? AND status = ?", user.Id, user.AuthVersion, common.UserStatusEnabled).
			UpdateColumn("auth_version", gorm.Expr("auth_version"))
		if result.Error != nil {
			return result.Error
		}
		// MySQL may report zero affected rows for a no-op lock update.
		var current User
		if err := tx.Select("id", "auth_version", "status").First(&current, user.Id).Error; err != nil {
			return err
		}
		if current.AuthVersion != user.AuthVersion || current.Status != common.UserStatusEnabled {
			return ErrAuthStateInvalid
		}
		now = time.Now().Unix()
		if err := checkUserSessionLimits(tx, &current, now); err != nil {
			return err
		}
		record.CreatedAt, record.ExpiresAt = now, now+int64(UserSessionLifetime/time.Second)
		return tx.Create(&record).Error
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

func ValidateUserSession(secret string, userID int) (*User, *UserSession, error) {
	if secret == "" || userID <= 0 {
		return nil, nil, ErrAuthStateInvalid
	}
	var record UserSession
	if err := DB.Where("secret_hash = ? AND user_id = ? AND revoked_at = 0 AND expires_at > ?",
		authSecretHash("session", secret), userID, time.Now().Unix()).First(&record).Error; err != nil {
		return nil, nil, err
	}
	user, err := GetUserById(userID, false)
	if err != nil {
		return nil, nil, err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != record.AuthVersion {
		return nil, nil, ErrAuthStateInvalid
	}
	return user, &record, nil
}

// AdoptLegacyUserSession accepts only an already validated signed cookie and
// only while the account is still at its initial security version. A stable
// cookie fingerprint prevents replay from minting a fresh record after logout.
func AdoptLegacyUserSession(user *User, signedCookie, ip, agent string) (string, error) {
	if user == nil || user.Id <= 0 || user.AuthVersion != 1 || user.Status != common.UserStatusEnabled || signedCookie == "" {
		return "", ErrAuthStateInvalid
	}
	allowed, err := allowLegacySessionAdoption(DB)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", ErrAuthStateInvalid
	}
	secret := "legacy_" + authSecretHash("legacy-cookie", signedCookie)
	if len(agent) > 512 {
		agent = agent[:512]
	}
	now := time.Now().Unix()
	record := UserSession{UserId: user.Id, SecretHash: authSecretHash("session", secret), AuthVersion: 1,
		CreatedAt: now, ExpiresAt: now + int64(UserSessionLifetime/time.Second), LoginMethod: "legacy", IP: ip, UserAgent: agent}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
			return err
		}
		var current User
		if err := tx.First(&current, user.Id).Error; err != nil {
			return err
		}
		if current.AuthVersion != 1 || current.Status != common.UserStatusEnabled || current.Username != user.Username {
			return ErrAuthStateInvalid
		}
		// An existing fingerprint (including a revocation tombstone) is never
		// reissued. Idempotent adoption must also work when the limit is full.
		var existing UserSession
		err := tx.Where("secret_hash = ?", record.SecretHash).First(&existing).Error
		if err == nil {
			if existing.UserId != user.Id {
				return ErrAuthStateInvalid
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now = time.Now().Unix()
		if err := checkUserSessionLimits(tx, &current, now); err != nil {
			return err
		}
		record.CreatedAt, record.ExpiresAt = now, now+int64(UserSessionLifetime/time.Second)
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

func RevokeUserSession(secret string, userID int) error {
	if secret == "" || userID <= 0 {
		return nil
	}
	return DB.Model(&UserSession{}).Where("secret_hash = ? AND user_id = ?", authSecretHash("session", secret), userID).
		Update("revoked_at", time.Now().Unix()).Error
}

func RevokeAllUserSessions(userID int) error {
	return DB.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
		"auth_version": gorm.Expr("auth_version + 1"), "access_token": nil,
	}).Error
}

func MarkUserSessionVerified(secret string, userID int, method string) (int64, error) {
	_, record, err := ValidateUserSession(secret, userID)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	result := DB.Model(&UserSession{}).Where("id = ? AND revoked_at = 0", record.Id).
		Updates(map[string]any{"verified_at": now, "verified_method": method})
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, ErrAuthStateInvalid
	}
	return now, nil
}

func ClearUserSessionVerification(secret string, userID int) error {
	return DB.Model(&UserSession{}).Where("secret_hash = ? AND user_id = ?", authSecretHash("session", secret), userID).
		Updates(map[string]any{"verified_at": 0, "verified_method": ""}).Error
}

// MigrateAuthenticationSchema must run after the normal User/Token migrations,
// before accepting requests. It is intentionally not invoked from package init.
func MigrateAuthenticationSchema(db *gorm.DB) error {
	if err := MigrateSchemaCompatibility(db); err != nil {
		return err
	}
	return migrateAdditive(db, modelsForMigration(migrationAuthentication)...)
}
