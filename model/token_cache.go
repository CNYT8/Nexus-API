package model

import (
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

func cacheSetToken(token Token) error {
	if !redisAvailable() {
		return nil
	}
	key := common.GenerateHMAC(token.Key)
	token.CacheExpiresAt = time.Now().Add(authCacheTTL()).Unix()
	mac, err := token.cacheMAC()
	if err != nil {
		return err
	}
	token.CacheMAC = mac
	token.Clean()
	return storeAuthCache(fmt.Sprintf("token:%s", key), &token, map[string]bool{"RemainQuota": true, "UsedQuota": true, "AccessedTime": true})
}

// cacheSetTokenSettings updates only non-accounting token fields. It preserves
// RemainQuota and UsedQuota maintained by atomic billing operations.
func cacheSetTokenRuntimeFields(token Token) error {
	if !redisAvailable() {
		return nil
	}
	key := common.GenerateHMAC(token.Key)
	cacheKey := fmt.Sprintf("token:%s", key)
	if err := common.RedisHSetField(cacheKey, "Status", strconv.Itoa(token.Status)); err != nil {
		return err
	}
	return common.RedisHSetField(cacheKey, "AccessedTime", strconv.FormatInt(token.AccessedTime, 10))
}

func cacheSetTokenSettings(token Token) error {
	if !redisAvailable() {
		return nil
	}
	key := common.GenerateHMAC(token.Key)
	cacheKey := fmt.Sprintf("token:%s", key)
	fields := map[string]string{
		"Name":                token.Name,
		"Status":              strconv.Itoa(token.Status),
		"ExpiredTime":         strconv.FormatInt(token.ExpiredTime, 10),
		"UnlimitedQuota":      strconv.FormatBool(token.UnlimitedQuota),
		"ModelLimitsEnabled":  strconv.FormatBool(token.ModelLimitsEnabled),
		"ModelLimits":         token.ModelLimits,
		"Group":               token.Group,
		"CrossGroupRetry":     strconv.FormatBool(token.CrossGroupRetry),
		"AutoGroups":          token.AutoGroups,
		"AllowUserQuotaQuery": strconv.FormatBool(token.AllowUserQuotaQuery),
		"AuthVersion":         strconv.FormatInt(token.AuthVersion, 10),
	}
	if token.AllowIps != nil {
		fields["AllowIps"] = *token.AllowIps
	} else {
		fields["AllowIps"] = ""
	}
	for field, value := range fields {
		if err := common.RedisHSetField(cacheKey, field, value); err != nil {
			return err
		}
	}
	return nil
}

func cacheDeleteToken(key string) error {
	key = common.GenerateHMAC(key)
	err := common.RedisDelKey(fmt.Sprintf("token:%s", key))
	if err != nil {
		return err
	}
	return nil
}

func cacheIncrTokenQuota(key string, increment int64) error {
	key = common.GenerateHMAC(key)
	err := common.RedisHIncrBy(fmt.Sprintf("token:%s", key), constant.TokenFiledRemainQuota, increment)
	if err != nil {
		return err
	}
	return nil
}

func cacheDecrTokenQuota(key string, decrement int64) error {
	return cacheIncrTokenQuota(key, -decrement)
}

func cacheSetTokenField(key string, field string, value string) error {
	key = common.GenerateHMAC(key)
	err := common.RedisHSetField(fmt.Sprintf("token:%s", key), field, value)
	if err != nil {
		return err
	}
	return nil
}

// CacheGetTokenByKey 从缓存中获取 token，如果缓存中不存在，则从数据库中获取
func cacheGetTokenByKey(key string) (*Token, error) {
	hmacKey := common.GenerateHMAC(key)
	if !redisAvailable() {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var token Token
	err := common.RedisHGetObj(fmt.Sprintf("token:%s", hmacKey), &token)
	if err != nil {
		return nil, err
	}
	token.Key = key
	mac, err := token.cacheMAC()
	if err != nil || token.Id <= 0 || !validAuthCacheMAC(mac, token.CacheMAC, token.CacheExpiresAt) {
		return nil, fmt.Errorf("token cache integrity check failed")
	}
	current := Token{}
	if err := DB.Where(commonKeyCol+" = ?", key).First(&current).Error; err != nil {
		return nil, err
	}
	current.CacheExpiresAt = token.CacheExpiresAt
	currentMAC, err := current.cacheMAC()
	if err != nil || !validAuthCacheMAC(currentMAC, token.CacheMAC, token.CacheExpiresAt) {
		return nil, fmt.Errorf("token cache version fence rejected stale metadata")
	}
	return &token, nil
}
