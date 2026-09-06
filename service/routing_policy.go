package service

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type routingCooldownKey struct {
	Channel int
	Model   string
}

var routingCooldowns sync.Map

// RecordRoutingFailure quarantines only this channel/model. It neither changes
// persistent channel status nor probes providers, and never stores error bodies.
func RecordRoutingFailure(c *gin.Context, channelID int, modelName string, status int, message string) {
	policy := operation_setting.GetRoutingPolicy()
	if !policy.Enabled {
		return
	}
	seconds := 0
	if status == 429 {
		seconds = policy.RateLimitCooldownSeconds
	}
	lower := strings.ToLower(message)
	for _, keyword := range policy.QuotaErrorKeywords {
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(keyword))) {
			seconds = policy.QuotaCooldownSeconds
			break
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
	key := routingCooldownKey{channelID, routingModelName(channelID, modelName)}
	until := now.Add(time.Duration(seconds) * time.Second)
	for {
		previous, exists := routingCooldowns.Load(key)
		if entries >= 10000 && !exists {
			return
		}
		if exists {
			if !previous.(time.Time).Before(until) {
				return
			}
			if !routingCooldowns.CompareAndSwap(key, previous, until) {
				continue
			}
		} else {
			if _, loaded := routingCooldowns.LoadOrStore(key, until); loaded {
				continue
			}
			logger.LogWarn(c, fmt.Sprintf("routing cooldown: channel=%d model=%s seconds=%d status=%d", channelID, modelName, seconds, status))
		}
		return
	}

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
	for _, idStr := range param.Ctx.GetStringSlice("use_channel") {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		excluded = append(excluded, id)
		channel, err := model.CacheGetChannel(id)
		if err == nil && channel.Tag != nil {
			attempts[*channel.Tag]++
		}
	}
	now := time.Now()
	routingCooldowns.Range(func(key, value any) bool {
		k := key.(routingCooldownKey)
		if !value.(time.Time).After(now) {
			routingCooldowns.CompareAndDelete(key, value)
		} else if k.Model == routingModelName(k.Channel, param.ModelName) {
			excluded = append(excluded, k.Channel)
		}
		return true
	})
	filters := append([]dto.ChannelFilter{}, GetChannelConstraints(param.Ctx).Filters...)
	filters = append(filters, dto.ChannelFilter{Kind: dto.FilterExcludedChannels, ExcludedChannelIDs: excluded})
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
		if attempts[tag] >= policy.MaxAttemptsPerTag {
			continue
		}
		tagFilters := append(append([]dto.ChannelFilter{}, filters...), dto.ChannelFilter{Kind: dto.FilterChannelTag, ChannelTag: tag})
		channel, err := model.GetRandomSatisfiedChannel(group, param.ModelName, 0, tagFilters)
		if err != nil {
			return nil, true, err
		}
		if channel != nil {
			if ok, _ := model.ChannelSatisfiesFilters(channel, param.ModelName, tagFilters); !ok {
				return nil, true, fmt.Errorf("routing candidate did not satisfy policy constraints")
			}
			candidates = append(candidates, channel)
		}
	}
	if len(candidates) == 0 {
		return nil, true, nil
	}
	remaining := common.RetryTimes - param.GetRetry() + 1
	if remaining <= 0 {
		return nil, true, nil
	}
	// After trying a tier, reserve one attempt per remaining provider. This keeps
	// a large free account pool from consuming the entire metered-fallback budget.
	for len(candidates) > 1 && candidates[0].Tag != nil && attempts[*candidates[0].Tag] > 0 && remaining < len(candidates) {
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
	if until, ok := routingCooldowns.Load(routingCooldownKey{preferred.Id, routingModelName(preferred.Id, modelName)}); ok && until.(time.Time).After(time.Now()) {
		return false
	}
	return true
}

// Aliases for the same upstream model share cooldowns without coupling unrelated
// models on a multi-model provider channel.
func routingModelName(channelID int, name string) string {
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel.ModelMapping == nil || *channel.ModelMapping == "" {
		return name
	}
	var mapping map[string]string
	if common.UnmarshalJsonStr(*channel.ModelMapping, &mapping) != nil {
		return name
	}
	if mapped := mapping[name]; mapped != "" {
		return mapped
	}
	return name
}
