package service

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
)

type routingCooldownKey struct {
	Resource string
	Model    string
}

var routingCooldowns sync.Map

const (
	// routingAuthCooldownSeconds 401/403（非配额类）的账号级冷却时长。
	routingAuthCooldownSeconds = 300
	// routingServerErrorCooldownSeconds 5xx/断流类的账号级冷却时长。
	routingServerErrorCooldownSeconds = 30
	// routingStreamShedCooldownSeconds 上游甩负载（200 空流/无终止事件）的模型级
	// 短冷却：账号没坏、只是瞬时过载，路由换账号几秒后即可回访。
	// 若按账号级长冷却，免费层大面积甩负载时整池蹲坑、请求直接 503
	//（2026-09-10 sensenova deepseek-v4-flash 实测）。
	routingStreamShedCooldownSeconds = 5
)

// routingMappingWarned 对"ModelMapping 损坏被路由层排除"的告警按渠道节流（10 分钟一次）。
var routingMappingWarned sync.Map

// routingWarnMappingExcluded 渠道因 ModelMapping 解析失败被路由层永久排除时告警。
// 之前该路径完全静默：渠道健康、配额充足却永远不被选中，无任何线索。
func routingWarnMappingExcluded(c *gin.Context, channelID int, err error) {
	now := time.Now()
	if last, ok := routingMappingWarned.Load(channelID); ok && now.Sub(last.(time.Time)) < 10*time.Minute {
		return
	}
	routingMappingWarned.Store(channelID, now)
	logger.LogWarn(routingRequestContext(c), fmt.Sprintf("routing excludes channel=%d: invalid model mapping: %v", channelID, err))
}

// ClearRoutingCooldownsForChannel 删除该渠道（含 account_resource 别名）的全部
// 本地路由冷却与连败计数。管理端手动重新启用/修复渠道后调用，避免残留冷却
// 继续跳过该渠道最长一个冷却周期。Redis 副本靠 TTL 自然过期（多实例最长延迟一个周期）。
func ClearRoutingCooldownsForChannel(channelID int) {
	resources := map[string]struct{}{fmt.Sprintf("channel:%d", channelID): {}}
	if channel, err := model.CacheGetChannel(channelID); err == nil {
		if s := channel.GetSetting().ProtocolRouting; s != nil && s.Enabled && s.AccountResource != "" {
			resources["account:"+s.AccountResource] = struct{}{}
		}
	}
	clearByResource := func(m *sync.Map) {
		m.Range(func(key, value any) bool {
			if _, ok := resources[key.(routingCooldownKey).Resource]; ok {
				m.Delete(key)
			}
			return true
		})
	}
	clearByResource(&routingCooldowns)
	clearByResource(&routingRateLimitStrikes)
	clearByResource(&routingAuthStrikes)
}

// quotaResetAtPattern 匹配上游配额错误消息内嵌的绝对重置时间，
// 如 "It will reset at 2026-09-10 13:59:10 +0800 CST"（Kimi 5 小时窗、火山周配额均已实测）。
var quotaResetAtPattern = regexp.MustCompile(`reset at (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) ([+-]\d{4})`)

// parseQuotaResetAt 从配额错误消息解析绝对重置时间；消息无可解析时间戳时
// 返回 ok=false，调用方回退固定冷却时长。
func parseQuotaResetAt(message string) (time.Time, bool) {
	m := quotaResetAtPattern.FindStringSubmatch(message)
	if m == nil {
		return time.Time{}, false
	}
	resetAt, err := time.Parse("2006-01-02 15:04:05 -0700", m[1]+" "+m[2])
	if err != nil {
		return time.Time{}, false
	}
	return resetAt, true
}

// StartRoutingBudget 记录本次请求的重试预算截止时间。预算只约束重试决策点
// （RoutingBudgetExceeded），不作为 ctx deadline——后者会在活跃流传输中途
// 取消请求（客户端收到无错误事件的静默截断）且与全部重试共享预算。
// 单次尝试的响应头等待由 Transport.ResponseHeaderTimeout 兜底，
// 流期间由 STREAMING_TIMEOUT 空闲看门狗兜底。
func StartRoutingBudget(c *gin.Context, timeoutSeconds int) {
	if timeoutSeconds <= 0 {
		return
	}
	common.SetContextKey(c, constant.ContextKeyRoutingBudgetDeadline, time.Now().Add(time.Duration(timeoutSeconds)*time.Second))
}

// RoutingBudgetExceeded 报告重试预算是否耗尽；未设置预算（非策略分组）返回 false。
func RoutingBudgetExceeded(c *gin.Context) bool {
	deadline := common.GetContextKeyTime(c, constant.ContextKeyRoutingBudgetDeadline)
	if deadline.IsZero() {
		return false
	}
	return !time.Now().Before(deadline)
}

// RecordRoutingFailure quarantines this account/model, or the whole account
// when configured. It never changes persistent channel status, probes providers,
// or stores error bodies.
func RecordRoutingFailure(c *gin.Context, channelID int, modelName string, status int, message string) {
	policy := operation_setting.GetRoutingPolicy()
	if !policy.Enabled {
		return
	}
	channel, err := model.CacheGetChannel(channelID)
	if err != nil {
		return
	}
	key, err := routingAccountKey(channel, modelName)
	if err != nil {
		return
	}
	seconds := 0
	quotaFailure := false
	lower := strings.ToLower(message)
	for _, keyword := range policy.QuotaErrorKeywords {
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(keyword))) {
			seconds = policy.QuotaCooldownSeconds
			quotaFailure = true
			break
		}
	}
	if quotaFailure {
		if resetAt, ok := parseQuotaResetAt(message); ok {
			// 消息自带精确重置时间：冷却到点（+60s 余量）精确恢复。
			// 上限 6×基础冷却：5 小时窗精确到点；周配额由 auto-ban 接管，无需超长冷却。
			precise := int(time.Until(resetAt).Seconds()) + 60
			seconds = min(max(precise, 60), policy.QuotaCooldownSeconds*6)
		}
	}
	if status == 429 && !quotaFailure {
		key.Model = ""
		seconds = routingNextRateLimitCooldown(c, key, policy)
	}
	if seconds <= 0 {
		switch {
		case status == 401 || status == 403:
			// 鉴权类（非配额）：密钥失效/权限/风控通常账号级且短时不可自愈。
			key.Model = ""
			seconds = routingAuthCooldownSeconds
		case status == 502 && (strings.Contains(lower, "ended without message_stop") ||
			strings.Contains(lower, "ended without a terminal event")):
			// 上游甩负载：模型级短冷却，保留账号服务其他模型的能力。
			seconds = routingStreamShedCooldownSeconds
		case status >= 500 && status <= 599:
			// 服务端/断流类：账号级短冷却，避免坏账号在每个新请求上被反复首选
			//（实证：无冷却时故障账号的错误分布极均匀）。
			key.Model = ""
			seconds = routingServerErrorCooldownSeconds
		}
	}
	if seconds <= 0 {
		return
	}
	now := time.Now()
	entries := 0
	routingCooldowns.Range(func(key, value any) bool {
		if !value.(time.Time).After(now) {
			routingCooldowns.CompareAndDelete(key, value)
		} else {
			entries++
		}
		return true
	})
	until := now.Add(time.Duration(seconds) * time.Second)
	if _, exists := routingCooldowns.Load(key); entries >= 10000 && !exists {
		return
	}
	routingStoreCooldown(c, key, until)
	logger.LogWarn(c, fmt.Sprintf("routing cooldown: channel=%d model=%s seconds=%d status=%d", channelID, modelName, seconds, status))
}

// SelectPolicyChannel returns handled=false for groups without a policy, keeping
// their existing routing behavior. A configured group fails closed on no match.
func SelectPolicyChannel(param *RetryParam, group string) (*model.Channel, bool, error) {
	policy := operation_setting.GetRoutingPolicy()
	order, configured := policy.GroupTagOrder[group]
	if !policy.Enabled || !configured {
		return nil, false, nil
	}
	if param.Ctx.Request != nil && param.Ctx.Request.Context().Err() != nil {
		return nil, true, param.Ctx.Request.Context().Err()
	}
	excluded := []int{}
	attempts := map[string]int{}
	usedResources := map[string]bool{}
	for _, idStr := range param.Ctx.GetStringSlice("use_channel") {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		excluded = append(excluded, id)
		channel, err := model.CacheGetChannel(id)
		if err == nil {
			key, _ := routingAccountKey(channel, param.ModelName)
			usedResources[key.Resource] = true
			if channel.Tag != nil {
				attempts[*channel.Tag]++
			}
		}
	}
	now := time.Now()
	routingCooldowns.Range(func(key, value any) bool {
		if !value.(time.Time).After(now) {
			routingCooldowns.CompareAndDelete(key, value)
		}
		return true
	})
	filters := append([]dto.ChannelFilter{}, GetChannelConstraints(param.Ctx).Filters...)
	filters = append(filters, dto.ChannelFilter{Kind: dto.FilterExcludedChannels, ExcludedChannelIDs: excluded})
	type candidateWithLoad struct {
		channel *model.Channel
		load    routingLoad
	}
	candidates := []*model.Channel{}
	lastTier := 0
	for rank, tag := range order {
		if attempts[tag] > 0 {
			lastTier = rank
		}
	}
	for rank, tag := range order {
		if rank < lastTier {
			continue
		}
		if policy.MaxAttemptsPerTag > 0 && attempts[tag] >= policy.MaxAttemptsPerTag {
			continue
		}
		tagFilters := append(append([]dto.ChannelFilter{}, filters...), dto.ChannelFilter{Kind: dto.FilterChannelTag, ChannelTag: tag})
		eligible := []candidateWithLoad{}
		var eligiblePriority int64
		capacity, tracksCapacity := policy.TagCapacity[tag]
		estimatedTokens := int64(common.GetContextKeyInt(param.Ctx, constant.ContextKeyEstimatedTokens))
		now := time.Now()
		for {
			channel, err := model.GetRandomSatisfiedChannel(group, param.ModelName, 0, tagFilters)
			if err != nil {
				return nil, true, err
			}
			if channel == nil {
				break
			}
			if ok, _ := model.ChannelSatisfiesFilters(channel, param.ModelName, tagFilters); !ok {
				return nil, true, fmt.Errorf("routing candidate did not satisfy policy constraints")
			}
			priority := channel.GetPriority()
			if len(eligible) > 0 && priority < eligiblePriority {
				break
			}
			key, mappingErr := routingAccountKey(channel, param.ModelName)
			if mappingErr != nil {
				routingWarnMappingExcluded(param.Ctx, channel.Id, mappingErr)
			}
			if mappingErr != nil || usedResources[key.Resource] || routingAccountCooling(param.Ctx, channel, param.ModelName, now) {
				tagFilters = append(tagFilters, dto.ChannelFilter{Kind: dto.FilterExcludedChannels, ExcludedChannelIDs: []int{channel.Id}})
				continue
			}
			load := routingLoad{}
			if tracksCapacity {
				loadKey := key
				loadKey.Model = ""
				load = routingReadLoad(param.Ctx, loadKey, capacity, now)
				if routingLoadSaturated(load, capacity, estimatedTokens) {
					tagFilters = append(tagFilters, dto.ChannelFilter{Kind: dto.FilterExcludedChannels, ExcludedChannelIDs: []int{channel.Id}})
					continue
				}
			}
			if len(eligible) == 0 {
				eligiblePriority = priority
			}
			eligible = append(eligible, candidateWithLoad{channel: channel, load: load})
			tagFilters = append(tagFilters, dto.ChannelFilter{Kind: dto.FilterExcludedChannels, ExcludedChannelIDs: []int{channel.Id}})
		}
		if len(eligible) > 0 {
			slices.SortStableFunc(eligible, func(a, b candidateWithLoad) int {
				return cmp.Compare(
					routingLoadUtilization(a.load, capacity),
					routingLoadUtilization(b.load, capacity),
				)
			})
			candidates = append(candidates, eligible[0].channel)
		}
	}
	if len(candidates) == 0 {
		return nil, true, nil
	}
	remaining := common.RetryTimes - param.GetRetry() + 1
	if policy.MaxTotalAttempts > 0 {
		remaining = policy.MaxTotalAttempts - len(param.Ctx.GetStringSlice("use_channel"))
	}
	if remaining <= 0 {
		return nil, true, nil
	}
	// After trying a tier, reserve one attempt per remaining provider. This keeps
	// a large free account pool from consuming the entire metered-fallback budget.
	for policy.MaxAttemptsPerTag > 0 && len(candidates) > 1 && candidates[0].Tag != nil && attempts[*candidates[0].Tag] > 0 && remaining < len(candidates) {
		candidates = candidates[1:]
	}
	return candidates[0], true, nil
}

func RoutingAffinityAllowed(c *gin.Context, preferred *model.Channel, modelName, group string) bool {
	candidate, handled, err := SelectPolicyChannel(&RetryParam{Ctx: c, TokenGroup: group, ModelName: modelName}, group)
	if !handled {
		return true
	}
	if err != nil || candidate == nil || preferred == nil || candidate.Tag == nil || preferred.Tag == nil || *candidate.Tag != *preferred.Tag {
		return false
	}
	key, mappingErr := routingAccountKey(preferred, modelName)
	if mappingErr != nil {
		routingWarnMappingExcluded(c, preferred.Id, mappingErr)
		return false
	}
	if routingAccountCooling(c, preferred, modelName, time.Now()) {
		return false
	}
	policy := operation_setting.GetRoutingPolicy()
	if capacity, ok := policy.TagCapacity[*preferred.Tag]; ok {
		loadKey := key
		loadKey.Model = ""
		load := routingReadLoad(c, loadKey, capacity, time.Now())
		estimatedTokens := int64(common.GetContextKeyInt(c, constant.ContextKeyEstimatedTokens))
		if routingLoadSaturated(load, capacity, estimatedTokens) {
			return false
		}
	}
	for _, idText := range c.GetStringSlice("use_channel") {
		id, err := strconv.Atoi(idText)
		if err != nil {
			continue
		}
		channel, err := model.CacheGetChannel(id)
		if err == nil {
			usedKey, _ := routingAccountKey(channel, modelName)
			if usedKey.Resource == key.Resource {
				return false
			}
		}
	}
	return true
}

func routingAccountCooling(c *gin.Context, channel *model.Channel, name string, now time.Time) bool {
	key, err := routingAccountKey(channel, name)
	if err != nil {
		return true
	}
	if key.Model == "" {
		key.Model, err = routingMappedModelName(channel, name)
		if err != nil {
			return true
		}
	}
	if until, ok := routingCooldowns.Load(key); ok && until.(time.Time).After(now) {
		return true
	}
	if routingRedisCooldown(c, key, now) {
		return true
	}
	// Honor an account-wide failure even when an alias declares model scope.
	accountKey := routingCooldownKey{Resource: key.Resource}
	if until, ok := routingCooldowns.Load(accountKey); ok && until.(time.Time).After(now) {
		return true
	}
	if routingRedisCooldown(c, accountKey, now) {
		return true
	}
	return false
}

// routingAccountKey keeps explicit account aliases together while preserving
// channel isolation when no administrator account identity was configured.
func routingAccountKey(channel *model.Channel, name string) (routingCooldownKey, error) {
	key := routingCooldownKey{Resource: fmt.Sprintf("channel:%d", channel.Id)}
	settings := channel.GetSetting().ProtocolRouting
	if settings != nil && settings.Enabled && settings.AccountResource != "" {
		key.Resource = "account:" + settings.AccountResource
	}
	mapped, err := routingMappedModelName(channel, name)
	if err != nil {
		return key, err
	}
	key.Model = mapped
	if settings != nil && settings.Enabled && settings.QuotaScope == "account" {
		key.Model = ""
	}
	return key, nil
}

// Match relay/helper.ModelMappedHelper's chain, suffix fallback, and terminal
// self-map semantics. This resolves cooldown identity only; it never rewrites a
// request or substitutes a model when the administrator supplied no mapping.
func routingMappedModelName(channel *model.Channel, name string) (string, error) {
	if channel.ModelMapping == nil || *channel.ModelMapping == "" {
		return name, nil
	}
	var mapping map[string]string
	if common.UnmarshalJsonStr(*channel.ModelMapping, &mapping) != nil {
		return "", fmt.Errorf("invalid routing model mapping")
	}
	current := name
	visited := map[string]bool{current: true}
	for {
		mapped, exists := mapping[current]
		base := hostreasoning.BaseModelName(current)
		if (!exists || mapped == "") && base != current {
			mapped, exists = mapping[base]
		}
		if !exists || mapped == "" || mapped == current {
			return current, nil
		}
		if visited[mapped] {
			return "", fmt.Errorf("routing model mapping contains a cycle")
		}
		visited[mapped] = true
		current = mapped
	}
}

// RoutingAttemptLimit is an absolute request budget, including the initial
// attempt. Zero preserves legacy retry behavior for unconfigured requests.
func RoutingAttemptLimit(c *gin.Context) int {
	policy := operation_setting.GetRoutingPolicy()
	if c == nil || !policy.Enabled || policy.MaxTotalAttempts == 0 {
		return 0
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "auto" {
		for _, candidate := range GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup)) {
			if _, ok := policy.GroupTagOrder[candidate]; ok {
				return policy.MaxTotalAttempts
			}
		}
		return 0
	}
	if _, ok := policy.GroupTagOrder[group]; ok {
		return policy.MaxTotalAttempts
	}
	return 0
}
