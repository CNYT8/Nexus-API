package controller

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Keep common's unexported serving key (including a nil original key) isolated
// without adding production reset hooks. This invokes only the already-built
// test executable, never go test/build or a network service. No t.Parallel.
func passwordCryptoControllerProcess(t *testing.T) bool {
	t.Helper()
	const marker = "NEXUS_PASSWORD_CRYPTO_CONTROLLER_TEST"
	if os.Getenv(marker) == t.Name() {
		return true
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.count=1", "-test.timeout=50s")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, marker+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, marker+"="+t.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated password crypto regression failed: %v\n%s", err, output)
	}
	return false
}

type passwordCryptoControllerSession struct {
	id, authSecret, pendingID any
	provider, flow, binding   string
	user                      *model.User
	err                       error
}

type passwordCryptoControllerFixture struct {
	db         *gorm.DB
	router     *gin.Engine
	privatePEM string
	publicKey  *rsa.PublicKey
	kid        string
	state      passwordCryptoControllerSession
}

func passwordCryptoControllerSetup(t *testing.T, withKey bool) *passwordCryptoControllerFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldSQLite, oldMySQL, oldPostgres := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	oldRedis, oldLogin, oldEncryption := common.RedisEnabled, common.PasswordLoginEnabled, common.PasswordLoginEncryptionEnabled
	oldMode := gin.Mode()
	oldLanguage, oldTranslate := i18n.DefaultLang, common.TranslateMessage
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = oldSQLite, oldMySQL, oldPostgres
		common.RedisEnabled, common.PasswordLoginEnabled, common.PasswordLoginEncryptionEnabled = oldRedis, oldLogin, oldEncryption
		gin.SetMode(oldMode)
		i18n.DefaultLang, common.TranslateMessage = oldLanguage, oldTranslate
		if err := pool.Close(); err != nil {
			t.Errorf("close password crypto memory database: %v", err)
		}
	})
	t.Setenv("ACCOUNT_PASSWORD_HASH_ALGORITHM", "bcrypt")
	t.Setenv("DEFAULT_LANGUAGE", i18n.LangEn)
	// The child process isolates i18n's private bundle and sync.Once. Restore
	// exposed globals per fixture, then rebind them even after Init is a no-op.
	require.NoError(t, i18n.Init())
	i18n.DefaultLang, common.TranslateMessage = i18n.LangEn, i18n.T
	model.DB, model.LOG_DB = db, db
	common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = true, false, false
	common.RedisEnabled, common.PasswordLoginEnabled, common.PasswordLoginEncryptionEnabled = false, true, true
	gin.SetMode(gin.TestMode)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TwoFA{}, &model.AuthFlow{}, &model.UserSession{}))
	f := &passwordCryptoControllerFixture{db: db, router: gin.New()}
	if withKey {
		f.installKey(t)
	}
	f.router.Use(sessions.Sessions("session", cookie.NewStore([]byte("password-crypto-controller-cookie-key"))))
	f.router.GET("/password-encryption", GetPasswordEncryptionKey)
	f.router.POST("/login", Login)
	f.router.GET("/inspect-pending", func(c *gin.Context) {
		session := sessions.Default(c)
		f.state = passwordCryptoControllerSession{
			id: session.Get("id"), authSecret: session.Get(service.AuthSessionKey), pendingID: session.Get("pending_user_id"),
		}
		f.state.provider, _ = session.Get("pending_login_provider").(string)
		f.state.flow, _ = session.Get("pending_login_flow").(string)
		f.state.binding, _ = session.Get("auth_browser_binding").(string)
		f.state.user, f.state.err = service.PendingLoginUser(c, false)
		c.Status(http.StatusNoContent)
	})
	return f
}

func (f *passwordCryptoControllerFixture) installKey(t *testing.T) {
	t.Helper()
	privatePEM, err := common.GeneratePasswordEncryptionPrivateKey()
	require.NoError(t, err)
	require.NoError(t, common.LoadPasswordEncryptionPrivateKey(privatePEM))
	f.privatePEM = privatePEM
	kid, publicPEM := common.PasswordEncryptionPublicKey()
	f.kid = kid
	block, _ := pem.Decode([]byte(publicPEM))
	require.NotNil(t, block)
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)
	var ok bool
	f.publicKey, ok = parsed.(*rsa.PublicKey)
	require.True(t, ok)
}

func (f *passwordCryptoControllerFixture) user(t *testing.T, password string) *model.User {
	t.Helper()
	hash, err := common.HashAccountPassword(password)
	require.NoError(t, err)
	user := &model.User{Username: "crypto-login-user", Password: hash, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 7}
	require.NoError(t, f.db.Create(user).Error)
	require.NoError(t, f.db.Create(&model.TwoFA{UserId: user.Id, Secret: "test-only-2fa-secret", IsEnabled: true}).Error)
	return user
}

func (f *passwordCryptoControllerFixture) encrypt(t *testing.T, password string, v2 bool) string {
	t.Helper()
	if !v2 {
		data, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, f.publicKey, []byte(password), nil)
		require.NoError(t, err)
		return base64.StdEncoding.EncodeToString(data)
	}
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, f.publicKey, key, []byte("password-v2"))
	require.NoError(t, err)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	_, err = rand.Read(nonce)
	require.NoError(t, err)
	data := gcm.Seal(nil, nonce, []byte(password), []byte("password-v2:"+f.kid))
	return strings.Join([]string{"v2", base64.StdEncoding.EncodeToString(wrapped), base64.StdEncoding.EncodeToString(nonce), base64.StdEncoding.EncodeToString(data)}, ".")
}

func (f *passwordCryptoControllerFixture) request(t *testing.T, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func (f *passwordCryptoControllerFixture) inspectSession(t *testing.T, recorder *httptest.ResponseRecorder) passwordCryptoControllerSession {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/inspect-pending", nil)
	// Browser binding and the pending flow each save a cookie. Browsers keep
	// the LAST Set-Cookie with a given name, not the first one in the response.
	var latest *http.Cookie
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "session" {
			latest = cookie
		}
	}
	if latest != nil {
		req.AddCookie(latest)
	}
	probe := httptest.NewRecorder()
	f.router.ServeHTTP(probe, req)
	require.Equal(t, http.StatusNoContent, probe.Code)
	return f.state
}

func passwordCryptoControllerNoLeak(t *testing.T, body string, secrets ...string) {
	t.Helper()
	for _, secret := range append(secrets, "BEGIN PRIVATE KEY", "crypto/rsa", "cipher: message authentication failed") {
		if secret != "" && strings.Contains(body, secret) {
			t.Fatal("public response disclosed password crypto input or internal detail")
		}
	}
}

func (f *passwordCryptoControllerFixture) assertPending(t *testing.T, recorder *httptest.ResponseRecorder, response map[string]any, user *model.User) {
	t.Helper()
	require.Equal(t, true, response["success"])
	require.Equal(t, map[string]any{"require_2fa": true}, response["data"])
	state := f.inspectSession(t, recorder)
	require.NoError(t, state.err)
	require.NotNil(t, state.user)
	require.Equal(t, user.Id, state.user.Id)
	require.Equal(t, user.AuthVersion, state.user.AuthVersion)
	require.Equal(t, user.Id, state.pendingID)
	require.Equal(t, "password", state.provider)
	require.NotEmpty(t, state.flow)
	require.NotEmpty(t, state.binding)
	require.Nil(t, state.id, "primary password must not establish a full login before 2FA")
	require.Nil(t, state.authSecret)
	flow, err := model.GetAuthFlow(state.flow, model.AuthFlowMatch{Purpose: "2fa_login", Provider: "password", Binding: state.binding, UserId: user.Id})
	require.NoError(t, err)
	require.Equal(t, user.AuthVersion, flow.AuthVersion)
	require.Zero(t, flow.ConsumedAt)
	require.Empty(t, flow.Payload)
	require.NotEqual(t, state.flow, flow.SecretHash)
	var flows, logins int64
	require.NoError(t, f.db.Model(&model.AuthFlow{}).Count(&flows).Error)
	require.EqualValues(t, 1, flows)
	require.NoError(t, f.db.Model(&model.UserSession{}).Count(&logins).Error)
	require.Zero(t, logins)
	passwordCryptoControllerNoLeak(t, recorder.Body.String(), f.privatePEM, user.Password, state.flow, state.binding, "test-only-2fa-secret")
}

func (f *passwordCryptoControllerFixture) assertRejected(t *testing.T, recorder *httptest.ResponseRecorder, response map[string]any) {
	t.Helper()
	require.Equal(t, false, response["success"])
	require.Len(t, response, 2, "failure response must contain only success and message")
	require.NotEmpty(t, response["message"])
	state := f.inspectSession(t, recorder)
	require.Error(t, state.err)
	require.Nil(t, state.id)
	require.Nil(t, state.authSecret)
	require.Nil(t, state.pendingID)
	require.Empty(t, state.flow)
	var flows, logins int64
	require.NoError(t, f.db.Model(&model.AuthFlow{}).Count(&flows).Error)
	require.NoError(t, f.db.Model(&model.UserSession{}).Count(&logins).Error)
	require.Zero(t, flows)
	require.Zero(t, logins)
}

func TestPasswordEncryptionKeyEndpointStates(t *testing.T) {
	if !passwordCryptoControllerProcess(t) {
		return
	}
	f := passwordCryptoControllerSetup(t, false)
	kid, publicKey := common.PasswordEncryptionPublicKey()
	require.Empty(t, kid)
	require.Empty(t, publicKey)
	for _, name := range []string{"disabled", "uninitialized", "enabled", "disabled_after_initialization"} {
		t.Run(name, func(t *testing.T) {
			common.PasswordLoginEncryptionEnabled = name == "uninitialized" || name == "enabled"
			if name == "enabled" {
				f.installKey(t)
			}
			recorder, response := f.request(t, http.MethodGet, "/password-encryption", nil)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.Equal(t, "no-cache", recorder.Header().Get("Pragma"))
			passwordCryptoControllerNoLeak(t, recorder.Body.String(), f.privatePEM)
			if name == "uninitialized" {
				require.Equal(t, false, response["success"])
				require.Len(t, response, 2)
				return
			}
			require.Equal(t, true, response["success"])
			if name != "enabled" {
				require.Equal(t, map[string]any{"enabled": false}, response["data"])
				return
			}
			kid, publicKey := common.PasswordEncryptionPublicKey()
			require.Equal(t, map[string]any{"enabled": true, "kid": kid, "public_key": publicKey}, response["data"])
		})
	}
}

func TestPasswordLoginEncryptionDisabledKeepsPlaintextCompatibility(t *testing.T) {
	if !passwordCryptoControllerProcess(t) {
		return
	}
	const password = "disabled-legacy-password"
	for _, name := range []string{"plaintext_only", "malformed_encrypted_fields_ignored", "encrypted_only_rejected", "valid_encryption_cannot_override_wrong_plaintext"} {
		t.Run(name, func(t *testing.T) {
			f := passwordCryptoControllerSetup(t, true)
			user := f.user(t, password)
			common.PasswordLoginEncryptionEnabled = false
			request := LoginRequest{Username: user.Username, Password: password}
			switch name {
			case "malformed_encrypted_fields_ignored":
				request.PasswordEncrypted, request.EncryptionKeyID = "broken-envelope", "unknown-kid"
			case "encrypted_only_rejected":
				request.Password = ""
				request.PasswordEncrypted, request.EncryptionKeyID = f.encrypt(t, password, false), f.kid
			case "valid_encryption_cannot_override_wrong_plaintext":
				request.Password = "wrong-plaintext-password"
				request.PasswordEncrypted, request.EncryptionKeyID = f.encrypt(t, password, true), f.kid
			}
			recorder, response := f.request(t, http.MethodPost, "/login", request)
			if name == "plaintext_only" || name == "malformed_encrypted_fields_ignored" {
				f.assertPending(t, recorder, response, user)
			} else {
				f.assertRejected(t, recorder, response)
			}
			passwordCryptoControllerNoLeak(t, recorder.Body.String(), password, user.Password, f.privatePEM, request.PasswordEncrypted)
		})
	}
}

func TestPasswordLoginEncryptionEnabledRejectsPlaintextAndInvalidCiphertext(t *testing.T) {
	if !passwordCryptoControllerProcess(t) {
		return
	}
	f := passwordCryptoControllerSetup(t, true)
	const password = "valid-plaintext-must-not-be-fallback"
	user := f.user(t, password)
	rsaPayload, v2Payload := f.encrypt(t, password, false), f.encrypt(t, password, true)
	parts := strings.Split(v2Payload, ".")
	damaged, err := base64.StdEncoding.DecodeString(parts[3])
	require.NoError(t, err)
	damaged[len(damaged)-1] ^= 1
	parts[3] = base64.StdEncoding.EncodeToString(damaged)
	wrongPassword := f.encrypt(t, "wrong-encrypted-password", false)
	baselineRecorder, baseline := f.request(t, http.MethodPost, "/login", LoginRequest{Username: user.Username, Password: password, PasswordEncrypted: wrongPassword, EncryptionKeyID: f.kid})
	f.assertRejected(t, baselineRecorder, baseline)
	passwordCryptoControllerNoLeak(t, baselineRecorder.Body.String(), password, user.Password, f.privatePEM, wrongPassword)
	for _, tc := range []struct {
		name, payload, kid string
		invalidParams      bool
	}{
		{"plaintext_only", "", "", true},
		{"missing_ciphertext", "", f.kid, true},
		{"missing_kid", rsaPayload, "", true},
		{"rsa_wrong_kid", rsaPayload, "unknown-key-id", false},
		{"v2_wrong_kid", v2Payload, "unknown-key-id", false},
		{"rsa_broken_ciphertext", base64.StdEncoding.EncodeToString(make([]byte, f.publicKey.Size())), f.kid, false},
		{"plaintext_as_ciphertext", password, f.kid, false},
		{"v2_broken_envelope", "v2.broken-secret-payload", f.kid, false},
		{"v2_tampered_tag", strings.Join(parts, "."), f.kid, false},
		{"v2_wrong_decrypted_password", f.encrypt(t, "wrong-encrypted-password", true), f.kid, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := LoginRequest{Username: user.Username, Password: password, PasswordEncrypted: tc.payload, EncryptionKeyID: tc.kid}
			recorder, response := f.request(t, http.MethodPost, "/login", request)
			f.assertRejected(t, recorder, response)
			if !tc.invalidParams {
				require.Equal(t, baseline["message"], response["message"], "decryption failures must be indistinguishable from wrong credentials")
			}
			passwordCryptoControllerNoLeak(t, recorder.Body.String(), password, user.Password, f.privatePEM, tc.payload, tc.kid)
		})
	}
}

func TestPasswordLoginEncryptedPasswordContinuesTwoFA(t *testing.T) {
	if !passwordCryptoControllerProcess(t) {
		return
	}
	for _, tc := range []struct {
		name      string
		v2        bool
		plaintext string
	}{
		{"rsa_encrypted_only", false, ""},
		{"rsa_ignores_plaintext", false, "incorrect-plaintext-must-be-ignored"},
		{"v2_encrypted_only", true, ""},
		{"v2_ignores_plaintext", true, "incorrect-plaintext-must-be-ignored"},
		{"v2_512_byte_chinese", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := passwordCryptoControllerSetup(t, true)
			password := "encrypted-login-password"
			if tc.name == "v2_512_byte_chinese" {
				t.Setenv("ACCOUNT_PASSWORD_HASH_ALGORITHM", "argon2id")
				password = strings.Repeat("𠀀", 128)
				require.Len(t, password, 512)
			}
			user := f.user(t, password)
			payload := f.encrypt(t, password, tc.v2)
			recorder, response := f.request(t, http.MethodPost, "/login", LoginRequest{Username: user.Username, Password: tc.plaintext, PasswordEncrypted: payload, EncryptionKeyID: f.kid})
			f.assertPending(t, recorder, response, user)
			passwordCryptoControllerNoLeak(t, recorder.Body.String(), password, payload, f.kid)
		})
	}
}
