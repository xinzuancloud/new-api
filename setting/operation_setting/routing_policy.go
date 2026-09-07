package operation_setting

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
)

const RoutingPolicyOptionKey = "RoutingPolicy"

// RoutingPolicy is published atomically. Callers must not mutate its maps/slices.
// Channel group/model permissions remain authoritative; tags only narrow them.
type RoutingPolicy struct {
	Enabled                  bool                `json:"enabled"`
	GroupTagOrder            map[string][]string `json:"group_tag_order"`
	MaxTotalAttempts         int                 `json:"max_total_attempts,omitempty"`
	MaxAttemptsPerTag        int                 `json:"max_attempts_per_tag"`
	RateLimitCooldownSeconds int                 `json:"rate_limit_cooldown_seconds"`
	QuotaCooldownSeconds     int                 `json:"quota_cooldown_seconds"`
	QuotaErrorKeywords       []string            `json:"quota_error_keywords"`
	RequestTimeoutSeconds    int                 `json:"request_timeout_seconds"`
}

var routingPolicy atomic.Pointer[RoutingPolicy]

func init() {
	routingPolicy.Store(&RoutingPolicy{GroupTagOrder: map[string][]string{}, MaxAttemptsPerTag: 3, RateLimitCooldownSeconds: 60, QuotaCooldownSeconds: 3600, QuotaErrorKeywords: []string{}, RequestTimeoutSeconds: 300})
}

func GetRoutingPolicy() *RoutingPolicy { return routingPolicy.Load() }

func RoutingPolicyJSON() string {
	data, _ := common.Marshal(GetRoutingPolicy())
	return string(data)
}

func ParseRoutingPolicy(value string) (*RoutingPolicy, error) {
	if len(value) > 65536 {
		return nil, fmt.Errorf("routing policy exceeds 64 KiB")
	}
	var policy RoutingPolicy
	if err := common.UnmarshalJsonStr(value, &policy); err != nil {
		return nil, err
	}
	if policy.MaxAttemptsPerTag < 0 || policy.MaxAttemptsPerTag > 256 {
		return nil, fmt.Errorf("attempts per tag must be between 0 and 256 (0 tries all available channels)")
	}
	if policy.MaxTotalAttempts < 0 || policy.MaxTotalAttempts > 1024 {
		return nil, fmt.Errorf("total attempts must be between 0 and 1024 (0 inherits the global retry limit)")
	}
	if policy.RateLimitCooldownSeconds < 0 || policy.RateLimitCooldownSeconds > 3600 {
		return nil, fmt.Errorf("rate limit cooldown must be between 0 and 3600 seconds")
	}
	if policy.QuotaCooldownSeconds < 0 || policy.QuotaCooldownSeconds > 604800 {
		return nil, fmt.Errorf("quota cooldown must be between 0 and 604800 seconds")
	}
	if policy.RequestTimeoutSeconds < 1 || policy.RequestTimeoutSeconds > 1800 {
		return nil, fmt.Errorf("request timeout must be between 1 and 1800 seconds")
	}
	if len(policy.GroupTagOrder) > 100 {
		return nil, fmt.Errorf("routing policy supports at most 100 groups")
	}
	for group, tags := range policy.GroupTagOrder {
		if group == "" || strings.TrimSpace(group) != group || len(group) > 128 || len(tags) == 0 || len(tags) > 32 {
			return nil, fmt.Errorf("each routing group requires a name and 1 to 32 tags")
		}
		seen := map[string]bool{}
		for _, tag := range tags {
			if tag == "" || strings.TrimSpace(tag) != tag || len(tag) > 128 || seen[tag] {
				return nil, fmt.Errorf("channel tags must be nonempty and unique within a group")
			}
			seen[tag] = true
		}
	}
	if len(policy.QuotaErrorKeywords) > 100 {
		return nil, fmt.Errorf("at most 100 quota keywords are allowed")
	}
	for _, keyword := range policy.QuotaErrorKeywords {
		if strings.TrimSpace(keyword) == "" || len(keyword) > 256 {
			return nil, fmt.Errorf("quota keywords must contain 1 to 256 characters")
		}
	}
	return &policy, nil
}

func UpdateRoutingPolicy(value string) error {
	policy, err := ParseRoutingPolicy(value)
	if err != nil {
		return err
	}
	routingPolicy.Store(policy)
	return nil
}
