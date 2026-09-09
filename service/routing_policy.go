package service

import (
	"cmp"
	"fmt"
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
	if status == 429 && !quotaFailure {
		key.Model = ""
		seconds = routingNextRateLimitCooldown(c, key, policy)
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
	if mappingErr != nil || routingAccountCooling(c, preferred, modelName, time.Now()) {
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
