package model

import (
	"context"
	"crypto/hmac"
	"fmt"
	"reflect"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func authCacheMAC(key string, metadata any) (string, error) {
	payload, err := common.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return common.GenerateHMACWithKey([]byte("nexus-auth-cache-v1:"+common.SessionSecret), key+"\x00"+string(payload)), nil
}

func validAuthCacheMAC(expected, actual string, expiresAt int64) bool {
	return expiresAt > time.Now().Unix() && expected != "" && hmac.Equal([]byte(expected), []byte(actual))
}

func authCacheTTL() time.Duration {
	seconds := common.RedisKeyCacheSeconds()
	if seconds <= 0 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

// Metadata and its MAC are installed atomically; live billing counters are
// initialized only when missing/invalid, never overwritten by a stale snapshot.
func storeAuthCache(key string, snapshot any, counters map[string]bool) error {
	value := reflect.Indirect(reflect.ValueOf(snapshot))
	typ := value.Type()
	args := []any{int64(authCacheTTL() / time.Second)}
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		name := typ.Field(i).Name
		if name == "DeletedAt" {
			continue
		}
		text := ""
		if field.Kind() == reflect.Pointer {
			if !field.IsNil() {
				text = fmt.Sprint(field.Elem().Interface())
			}
		} else {
			text = fmt.Sprint(field.Interface())
		}
		preserve := "0"
		if counters[name] {
			preserve = "1"
		}
		args = append(args, name, text, preserve)
	}
	const script = `
for i = 2, #ARGV, 3 do
  if ARGV[i+2] ~= '1' or not string.match(redis.call('HGET', KEYS[1], ARGV[i]) or '', '^%-?%d+$') then
    redis.call('HSET', KEYS[1], ARGV[i], ARGV[i+1])
  end
end
redis.call('HDEL', KEYS[1], 'DeletedAt')
redis.call('EXPIRE', KEYS[1], ARGV[1])
return 1`
	return common.RDB.Eval(context.Background(), script, []string{key}, args...).Err()
}

func (user UserBase) cacheMAC() (string, error) {
	user.Quota = 0
	user.CacheMAC = ""
	return authCacheMAC(getUserCacheKey(user.Id), struct {
		User      UserBase
		Version   int64
		ExpiresAt int64
	}{user, user.AuthVersion, user.CacheExpiresAt})
}

func (token Token) cacheMAC() (string, error) {
	key := fmt.Sprintf("token:%s", common.GenerateHMAC(token.Key))
	token.Key, token.CacheMAC = "", ""
	token.RemainQuota, token.UsedQuota, token.AccessedTime = 0, 0, 0
	if token.AllowIps != nil && *token.AllowIps == "" {
		token.AllowIps = nil
	}
	return authCacheMAC(key, struct {
		Token      Token
		AutoGroups string
		Version    int64
		ExpiresAt  int64
	}{token, token.AutoGroups, token.AuthVersion, token.CacheExpiresAt})
}
