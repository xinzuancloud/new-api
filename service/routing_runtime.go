package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const routingRedisNamespace = "new-api:routing:v1"

type routingLoad struct {
	Requests int64
	Tokens   int64
}

type routingLoadWindow struct {
	mu      sync.Mutex
	buckets map[int64]routingLoad
}

type routingStrike struct {
	Count     int64
	ExpiresAt time.Time
}

var routingLoadBuckets sync.Map
var routingRateLimitStrikes sync.Map

// routingAuthStrikes 记录账号级 401/403 连败（30 分钟窗口），
// 供整渠道 auto-ban 门控：鉴权错误先走账号级短冷却，连败达标才允许禁用。
var routingAuthStrikes sync.Map

type routingAuthStrike struct {
	Count     int
	ExpiresAt time.Time
}

// AllowAutoBanChannel 门控 401/403 触发的整渠道自动禁用：同一账号 30 分钟内
// 累计 3 次鉴权失败才放行 auto-ban，其余状态码维持原语义立即放行。
// 一次成功请求（RecordRoutingSuccess）清零连败。
func AllowAutoBanChannel(c *gin.Context, channelID int, status int) bool {
	if status != 401 && status != 403 {
		return true
	}
	channel, err := model.CacheGetChannel(channelID)
	if err != nil {
		return true
	}
	key, _ := routingAccountKey(channel, "")
	key.Model = ""
	now := time.Now()
	for {
		previousValue, exists := routingAuthStrikes.Load(key)
		previous := routingAuthStrike{}
		if exists {
			previous = previousValue.(routingAuthStrike)
		}
		if previous.ExpiresAt.Before(now) {
			previous.Count = 0
		}
		next := routingAuthStrike{Count: previous.Count + 1, ExpiresAt: now.Add(30 * time.Minute)}
		if exists {
			if !routingAuthStrikes.CompareAndSwap(key, previousValue, next) {
				continue
			}
		} else if _, loaded := routingAuthStrikes.LoadOrStore(key, next); loaded {
			continue
		}
		if next.Count < 3 {
			logger.LogWarn(routingRequestContext(c), fmt.Sprintf("auth failure %d/3 within 30m for channel=%d status=%d: auto-ban deferred to routing cooldown", next.Count, channelID, status))
			return false
		}
		return true
	}
}

const routingLoadRecordScript = `
local second = ARGV[1]
local window = tonumber(ARGV[2])
redis.call('HINCRBY', KEYS[1], second .. ':r', 1)
redis.call('HINCRBY', KEYS[1], second .. ':t', ARGV[3])
for _, field in ipairs(redis.call('HKEYS', KEYS[1])) do
  local separator = string.find(field, ':')
  local bucket = tonumber(string.sub(field, 1, separator - 1))
  if bucket <= tonumber(second) - window then
    redis.call('HDEL', KEYS[1], field)
  end
end
redis.call('EXPIRE', KEYS[1], window * 2)
return 1
`

const routingCooldownStoreScript = `
local previous = redis.call('GET', KEYS[1])
if previous and tonumber(previous) >= tonumber(ARGV[1]) then
  return previous
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return ARGV[1]
`

const routingStrikeResetScript = `
if redis.call('EXISTS', KEYS[1]) == 1 then
  return 0
end
redis.call('DEL', KEYS[2])
return 1
`

func routingRequestContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}

func routingRedisIdentity(key routingCooldownKey) string {
	sum := sha256.Sum256([]byte(key.Resource + "\x00" + key.Model))
	return hex.EncodeToString(sum[:16])
}

func routingCooldownRedisKey(key routingCooldownKey) string {
	return routingRedisNamespace + ":cooldown:" + routingRedisIdentity(key)
}

func routingLoadRedisKey(key routingCooldownKey) string {
	return routingRedisNamespace + ":load:" + routingRedisIdentity(key)
}

func routingStrikeRedisKey(key routingCooldownKey) string {
	return routingRedisNamespace + ":strike:" + routingRedisIdentity(key)
}

func routingStoreCooldown(c *gin.Context, key routingCooldownKey, until time.Time) {
	for {
		previous, exists := routingCooldowns.Load(key)
		if exists && !previous.(time.Time).Before(until) {
			until = previous.(time.Time)
			break
		}
		if exists && !routingCooldowns.CompareAndSwap(key, previous, until) {
			continue
		}
		if !exists {
			if _, loaded := routingCooldowns.LoadOrStore(key, until); loaded {
				continue
			}
		}
		break
	}
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ttl := time.Until(until)
	if ttl <= 0 {
		return
	}
	_, err := common.RDB.Eval(
		routingRequestContext(c),
		routingCooldownStoreScript,
		[]string{routingCooldownRedisKey(key)},
		until.UnixMilli(),
		ttl.Milliseconds(),
	).Result()
	if err != nil {
		logger.LogError(routingRequestContext(c), fmt.Sprintf("routing cooldown Redis write failed: %v", err))
	}
}

func routingRedisCooldown(c *gin.Context, key routingCooldownKey, now time.Time) bool {
	if !common.RedisEnabled || common.RDB == nil {
		return false
	}
	value, err := common.RDB.Get(routingRequestContext(c), routingCooldownRedisKey(key)).Int64()
	if err != nil {
		if err != redis.Nil {
			logger.LogError(routingRequestContext(c), fmt.Sprintf("routing cooldown Redis read failed: %v", err))
		}
		return false
	}
	until := time.UnixMilli(value)
	if !until.After(now) {
		return false
	}
	routingCooldowns.Store(key, until)
	return true
}

func routingNextRateLimitCooldown(c *gin.Context, key routingCooldownKey, policy *operation_setting.RoutingPolicy) int {
	if policy.RateLimitCooldownSeconds <= 0 {
		return 0
	}
	var count int64
	if common.RedisEnabled && common.RDB != nil {
		redisKey := routingStrikeRedisKey(key)
		pipeline := common.RDB.TxPipeline()
		increment := pipeline.Incr(routingRequestContext(c), redisKey)
		pipeline.Expire(routingRequestContext(c), redisKey, time.Duration(policy.RateLimitMaxCooldownSeconds*2)*time.Second)
		if _, err := pipeline.Exec(routingRequestContext(c)); err == nil {
			count = increment.Val()
		} else {
			logger.LogError(routingRequestContext(c), fmt.Sprintf("routing rate-limit strike Redis write failed: %v", err))
		}
	}
	if count == 0 {
		now := time.Now()
		for {
			previousValue, exists := routingRateLimitStrikes.Load(key)
			previous := routingStrike{}
			if exists {
				previous = previousValue.(routingStrike)
			}
			if previous.ExpiresAt.Before(now) {
				previous.Count = 0
			}
			next := routingStrike{
				Count:     previous.Count + 1,
				ExpiresAt: now.Add(time.Duration(policy.RateLimitMaxCooldownSeconds*2) * time.Second),
			}
			if exists {
				if !routingRateLimitStrikes.CompareAndSwap(key, previousValue, next) {
					continue
				}
			} else if _, loaded := routingRateLimitStrikes.LoadOrStore(key, next); loaded {
				continue
			}
			count = next.Count
			break
		}
	}
	seconds := int64(policy.RateLimitCooldownSeconds)
	for i := int64(1); i < count && seconds < int64(policy.RateLimitMaxCooldownSeconds); i++ {
		seconds = min(seconds*2, int64(policy.RateLimitMaxCooldownSeconds))
	}
	return int(seconds)
}

func routingResetRateLimitStrike(c *gin.Context, key routingCooldownKey) {
	if until, ok := routingCooldowns.Load(key); ok && until.(time.Time).After(time.Now()) {
		return
	}
	if common.RedisEnabled && common.RDB != nil {
		cleared, err := common.RDB.Eval(
			routingRequestContext(c),
			routingStrikeResetScript,
			[]string{routingCooldownRedisKey(key), routingStrikeRedisKey(key)},
		).Int()
		if err != nil {
			logger.LogError(routingRequestContext(c), fmt.Sprintf("routing rate-limit strike Redis delete failed: %v", err))
			return
		}
		if cleared == 0 {
			return
		}
	}
	routingRateLimitStrikes.Delete(key)
}

func routingRecordLoad(c *gin.Context, key routingCooldownKey, capacity operation_setting.RoutingTagCapacity, tokens int64, now time.Time) {
	if tokens < 0 {
		tokens = 0
	}
	if common.RedisEnabled && common.RDB != nil {
		if _, err := common.RDB.Eval(
			routingRequestContext(c),
			routingLoadRecordScript,
			[]string{routingLoadRedisKey(key)},
			now.Unix(),
			capacity.WindowSeconds,
			tokens,
		).Result(); err == nil {
			return
		} else {
			logger.LogError(routingRequestContext(c), fmt.Sprintf("routing load Redis write failed: %v", err))
		}
	}
	value, _ := routingLoadBuckets.LoadOrStore(key, &routingLoadWindow{buckets: map[int64]routingLoad{}})
	window := value.(*routingLoadWindow)
	window.mu.Lock()
	defer window.mu.Unlock()
	cutoff := now.Unix() - int64(capacity.WindowSeconds)
	for second := range window.buckets {
		if second <= cutoff {
			delete(window.buckets, second)
		}
	}
	bucket := window.buckets[now.Unix()]
	bucket.Requests++
	bucket.Tokens += tokens
	window.buckets[now.Unix()] = bucket
}

func routingReadLoad(c *gin.Context, key routingCooldownKey, capacity operation_setting.RoutingTagCapacity, now time.Time) routingLoad {
	if common.RedisEnabled && common.RDB != nil {
		values, err := common.RDB.HGetAll(routingRequestContext(c), routingLoadRedisKey(key)).Result()
		if err == nil {
			return sumRoutingLoad(values, int64(capacity.WindowSeconds), now.Unix())
		}
		logger.LogError(routingRequestContext(c), fmt.Sprintf("routing load Redis read failed: %v", err))
	}
	value, ok := routingLoadBuckets.Load(key)
	if !ok {
		return routingLoad{}
	}
	window := value.(*routingLoadWindow)
	window.mu.Lock()
	defer window.mu.Unlock()
	cutoff := now.Unix() - int64(capacity.WindowSeconds)
	var total routingLoad
	for second, bucket := range window.buckets {
		if second <= cutoff {
			delete(window.buckets, second)
			continue
		}
		total.Requests += bucket.Requests
		total.Tokens += bucket.Tokens
	}
	return total
}

func sumRoutingLoad(values map[string]string, window, now int64) routingLoad {
	cutoff := now - window
	var total routingLoad
	for field, value := range values {
		secondText, kind, ok := strings.Cut(field, ":")
		if !ok {
			continue
		}
		second, err := strconv.ParseInt(secondText, 10, 64)
		if err != nil || second <= cutoff || second > now {
			continue
		}
		amount, err := strconv.ParseInt(value, 10, 64)
		if err != nil || amount < 0 {
			continue
		}
		switch kind {
		case "r":
			total.Requests += amount
		case "t":
			total.Tokens += amount
		}
	}
	return total
}

func routingLoadSaturated(load routingLoad, capacity operation_setting.RoutingTagCapacity, estimatedTokens int64) bool {
	if capacity.MaxRequests > 0 && load.Requests+1 > capacity.MaxRequests {
		return true
	}
	// An idle account receives one probe even when a single prompt exceeds the
	// configured window. This avoids declaring every account unavailable without
	// observing whether the provider accepts that request size.
	return capacity.MaxInputTokens > 0 && load.Requests > 0 && load.Tokens+estimatedTokens > capacity.MaxInputTokens
}

func routingLoadUtilization(load routingLoad, capacity operation_setting.RoutingTagCapacity) float64 {
	var utilization float64
	if capacity.MaxRequests > 0 {
		utilization = float64(load.Requests) / float64(capacity.MaxRequests)
	}
	if capacity.MaxInputTokens > 0 {
		utilization = max(utilization, float64(load.Tokens)/float64(capacity.MaxInputTokens))
	}
	return utilization
}

// RecordRoutingAttempt records local RPM/TPM capacity immediately before the
// upstream request so later selectors can spread work across accounts.
func RecordRoutingAttempt(c *gin.Context, channelID int, modelName string, estimatedTokens int) {
	policy := operation_setting.GetRoutingPolicy()
	if !policy.Enabled {
		return
	}
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel.Tag == nil {
		return
	}
	capacity, ok := policy.TagCapacity[*channel.Tag]
	if !ok {
		return
	}
	key, err := routingAccountKey(channel, modelName)
	if err != nil {
		return
	}
	key.Model = ""
	routingRecordLoad(c, key, capacity, int64(estimatedTokens), time.Now())
}

// RecordRoutingSuccess clears exponential 429 history after the account has
// completed a real request successfully.
func RecordRoutingSuccess(c *gin.Context, channelID int, modelName string) {
	policy := operation_setting.GetRoutingPolicy()
	if !policy.Enabled {
		return
	}
	channel, err := model.CacheGetChannel(channelID)
	if err != nil {
		return
	}
	// 鉴权连败清零与 tag/策略无关：一次成功即证明账号恢复
	authKey, _ := routingAccountKey(channel, "")
	authKey.Model = ""
	routingAuthStrikes.Delete(authKey)
	if channel.Tag == nil || !routingPolicyUsesTag(policy, *channel.Tag) {
		return
	}
	key, err := routingAccountKey(channel, modelName)
	if err == nil {
		key.Model = ""
		routingResetRateLimitStrike(c, key)
	}
}

func routingPolicyUsesTag(policy *operation_setting.RoutingPolicy, tag string) bool {
	for _, order := range policy.GroupTagOrder {
		if slices.Contains(order, tag) {
			return true
		}
	}
	return false
}
