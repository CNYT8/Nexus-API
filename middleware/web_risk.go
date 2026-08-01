package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	WebRiskVerificationRequiredCode = "WEB_RISK_VERIFICATION_REQUIRED"
	webRiskObservationInterval      = int64(60)
	webRiskChallengeCacheKeyPrefix  = "nexus:web-risk:challenged:"
	webRiskSessionIPKey             = "web_risk_ip"
	webRiskSessionObservedAtKey     = "web_risk_observed_at"
)

type webRiskChallengeCacheEntry struct {
	challenged bool
	expiresAt  int64
}

var webRiskChallengeCache sync.Map

func webRiskExemptPath(path string) bool {
	return path == "/api/user/web-risk/status" ||
		path == "/api/user/web-risk/verify" ||
		path == "/api/user/logout"
}

func webRiskCacheKey(userId int) string {
	return fmt.Sprintf("%s%d", webRiskChallengeCacheKeyPrefix, userId)
}

func cacheWebRiskChallenge(userId int, challenged bool) {
	if !challenged {
		webRiskChallengeCache.Delete(userId)
		return
	}
	now := time.Now().Unix()
	ttl := int64(5)
	webRiskChallengeCache.Store(userId, webRiskChallengeCacheEntry{
		challenged: challenged,
		expiresAt:  now + ttl,
	})
	if common.RedisEnabled && common.RDB != nil {
		expiration := 30 * 24 * time.Hour
		if err := common.RedisSet(webRiskCacheKey(userId), strconv.FormatBool(challenged), expiration); err != nil {
			common.SysLog("failed to cache web risk challenge: " + err.Error())
		}
	}
}

func loadWebRiskChallenge(userId int) (bool, error) {
	now := time.Now().Unix()
	if value, ok := webRiskChallengeCache.Load(userId); ok {
		entry := value.(webRiskChallengeCacheEntry)
		if entry.expiresAt > now {
			return entry.challenged, nil
		}
		webRiskChallengeCache.Delete(userId)
	}
	if common.RedisEnabled && common.RDB != nil {
		value, err := common.RedisGet(webRiskCacheKey(userId))
		if err == nil {
			challenged, parseErr := strconv.ParseBool(value)
			// Only positive states are shared across instances. A cached false
			// must never overwrite or hide a challenge created by another node.
			if parseErr == nil && challenged {
				status, statusErr := model.GetWebRiskStatus(userId)
				if statusErr != nil {
					return false, statusErr
				}
				if !status.Challenged {
					if deleteErr := common.RedisDelKey(webRiskCacheKey(userId)); deleteErr != nil {
						common.SysLog("failed to clear stale web risk challenge cache: " + deleteErr.Error())
					}
				}
				cacheWebRiskChallenge(userId, status.Challenged)
				return status.Challenged, nil
			}
		} else if !errors.Is(err, redis.Nil) {
			common.SysLog("failed to load web risk challenge cache: " + err.Error())
		}
	}
	status, err := model.GetWebRiskStatus(userId)
	if err != nil {
		return false, err
	}
	cacheWebRiskChallenge(userId, status.Challenged)
	return status.Challenged, nil
}

func ClearWebRiskChallengeCache(userId int) {
	webRiskChallengeCache.Delete(userId)
	if common.RedisEnabled && common.RDB != nil {
		if err := common.RedisDelKey(webRiskCacheKey(userId)); err != nil {
			common.SysLog("failed to clear web risk challenge cache: " + err.Error())
		}
	}
}

func UpdateWebRiskChallengeCache(userId int, challenged bool) {
	cacheWebRiskChallenge(userId, challenged)
}

func respondWebRiskChallenge(c *gin.Context) {
	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"message": common.TranslateMessage(c, i18n.MsgWebRiskVerificationRequired),
		"code":    WebRiskVerificationRequiredCode,
		"data": gin.H{
			"turnstile_site_key": common.TurnstileSiteKey,
		},
	})
	c.Abort()
}

func enforceWebRisk(c *gin.Context, userId int) bool {
	if !common.TurnstileConfigured() || webRiskExemptPath(c.Request.URL.Path) {
		return true
	}
	challenged, err := loadWebRiskChallenge(userId)
	if err != nil {
		common.SysLog("failed to load web risk state: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgDatabaseError),
		})
		c.Abort()
		return false
	}
	if challenged {
		respondWebRiskChallenge(c)
		return false
	}

	fingerprint, err := model.WebRiskIPFingerprint(c.ClientIP())
	if err != nil {
		common.SysLog("failed to fingerprint web risk IP: " + err.Error())
		return true
	}
	now := time.Now().Unix()
	session := sessions.Default(c)
	lastFingerprint, _ := session.Get(webRiskSessionIPKey).(string)
	lastObservedAt, _ := session.Get(webRiskSessionObservedAtKey).(int64)
	if lastFingerprint == fingerprint && now-lastObservedAt < webRiskObservationInterval {
		return true
	}

	status, err := model.ObserveWebRiskIP(userId, c.ClientIP())
	if err != nil {
		common.SysLog("failed to observe web risk IP: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgDatabaseError),
		})
		c.Abort()
		return false
	}
	session.Set(webRiskSessionIPKey, fingerprint)
	session.Set(webRiskSessionObservedAtKey, now)
	if err := session.Save(); err != nil {
		common.SysLog("failed to save web risk session: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgUserSessionSaveFailed),
		})
		c.Abort()
		return false
	}
	cacheWebRiskChallenge(userId, status.Challenged)
	if status.Challenged {
		respondWebRiskChallenge(c)
		return false
	}
	return true
}
