// Package protocol_setting publishes immutable supplier/product policy snapshots.
package protocol_setting

import (
	"crypto/sha256"
	"fmt"
	"github.com/QuantumNous/new-api/constant"
	"net/url"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

const OptionKey = "ProtocolProfiles"

type Profile struct {
	Name         string                             `json:"name"`
	Description  string                             `json:"description,omitempty"`
	ChannelTypes []int                              `json:"channel_types,omitempty"`
	BaseURLs     []string                           `json:"base_urls,omitempty"`
	Defaults     dto.ProtocolModelPolicy            `json:"defaults"`
	Models       map[string]dto.ProtocolModelPolicy `json:"models,omitempty"`
}
type Catalog map[string]Profile

var snapshot atomic.Pointer[Catalog]

func init() { c := Catalog{}; snapshot.Store(&c) }

// Get returns a read-only snapshot. Callers that mutate must Parse(JSON(Get())).
func Get() Catalog { return *snapshot.Load() }
func JSON(c Catalog) string {
	if c == nil {
		return "{}"
	}
	b, _ := common.Marshal(c)
	return string(b)
}
func Revision(c Catalog) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(JSON(c)))) }
func ProfileRevision(p Profile) string {
	b, _ := common.Marshal(p)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func Update(value string) error {
	c, e := Parse(value)
	if e != nil {
		return e
	}
	snapshot.Store(&c)
	return nil
}
func Parse(value string) (Catalog, error) {
	if len(value) > 60*1024 {
		return nil, fmt.Errorf("protocol catalog exceeds 60 KiB")
	}
	c := Catalog{}
	if common.UnmarshalJsonStr(value, &c) != nil || c == nil {
		return nil, fmt.Errorf("invalid protocol catalog")
	}
	if len(c) > 32 {
		return nil, fmt.Errorf("too many protocol profiles")
	}
	for id, p := range c {
		if !dto.ValidProtocolProfileID(id) || strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 || len(p.Description) > 2048 || len(p.ChannelTypes) > 64 || len(p.BaseURLs) > 64 {
			return nil, fmt.Errorf("invalid protocol profile")
		}
		for _, kind := range p.ChannelTypes {
			if kind <= 0 {
				return nil, fmt.Errorf("invalid profile channel type")
			}
		}
		for _, base := range p.BaseURLs {
			if _, ok := constant.ChannelSpecialBases[base]; ok {
				continue
			}
			u, e := url.Parse(base)
			if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
				return nil, fmt.Errorf("invalid profile base URL")
			}
		}
		if len(p.Defaults.EndpointOverrides) > 0 {
			return nil, fmt.Errorf("profile defaults must be complete")
		}
		if e := p.Defaults.Validate(); e != nil {
			return nil, e
		}
		if len(p.Models) > 256 {
			return nil, fmt.Errorf("too many profile models")
		}
		for name, override := range p.Models {
			if name == "" || len(name) > 256 || strings.TrimSpace(name) != name || strings.IndexFunc(name, unicode.IsControl) >= 0 {
				return nil, fmt.Errorf("invalid profile model identifier")
			}
			if _, e := p.Defaults.Merge(override); e != nil {
				return nil, e
			}
		}
	}
	return c, nil
}
func (c Catalog) Resolve(settings *dto.ProtocolRoutingSettings, model string) (dto.ProtocolModelPolicy, error) {
	if settings == nil {
		return dto.ProtocolModelPolicy{}, fmt.Errorf("protocol settings missing")
	}
	if e := settings.Validate(); e != nil {
		return dto.ProtocolModelPolicy{}, e
	}
	policy := settings.Defaults
	if settings.Profile != "" {
		p, ok := c[settings.Profile]
		if !ok {
			return policy, fmt.Errorf("protocol profile not found")
		}
		policy = p.Defaults
		var e error
		if override, ok := p.Models[model]; ok {
			policy, e = policy.Merge(override)
			if e != nil {
				return policy, e
			}
		}
		policy, e = policy.Merge(settings.Defaults)
		if e != nil {
			return policy, e
		}
	}
	if override, ok := settings.Models[model]; ok {
		return policy.Merge(override)
	}
	return policy, policy.Validate()
}
func (c Catalog) ValidateProduct(settings *dto.ProtocolRoutingSettings, channelType int, baseURL string) error {
	if settings == nil {
		return nil
	}
	if e := settings.Validate(); e != nil {
		return e
	}
	if settings.Profile == "" {
		return nil
	}
	p, ok := c[settings.Profile]
	if !ok {
		return fmt.Errorf("protocol profile not found")
	}
	if len(p.ChannelTypes) > 0 {
		found := false
		for _, kind := range p.ChannelTypes {
			if kind == channelType {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("protocol profile channel type mismatch")
		}
	}
	if len(p.BaseURLs) > 0 {
		found := false
		for _, base := range p.BaseURLs {
			if sameProtocolProductBase(base, baseURL) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("protocol profile base URL mismatch")
		}
	}
	return nil
}

func (c Catalog) ValidateChannel(settings *dto.ProtocolRoutingSettings, channelType int, baseURL string) error {
	if err := c.ValidateProduct(settings, channelType, baseURL); err != nil {
		return err
	}
	if settings == nil || settings.Profile == "" {
		return nil
	}
	p := c[settings.Profile]
	if _, e := c.Resolve(settings, ""); e != nil {
		return e
	}
	for name := range p.Models {
		if _, e := c.Resolve(settings, name); e != nil {
			return e
		}
	}
	for name := range settings.Models {
		if name == "" || len(name) > 256 || strings.TrimSpace(name) != name || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return fmt.Errorf("invalid channel model identifier")
		}
		if _, e := c.Resolve(settings, name); e != nil {
			return e
		}
	}
	return nil
}

func sameProtocolProductBase(a, b string) bool {
	a = strings.TrimRight(a, "/")
	b = strings.TrimRight(b, "/")
	if a == b {
		return true
	}
	if special, ok := constant.ChannelSpecialBases[a]; ok {
		if strings.TrimRight(special.ClaudeBaseURL, "/") == b || strings.TrimRight(special.OpenAIBaseURL, "/") == b {
			return true
		}
	}
	if special, ok := constant.ChannelSpecialBases[b]; ok {
		if strings.TrimRight(special.ClaudeBaseURL, "/") == a || strings.TrimRight(special.OpenAIBaseURL, "/") == a {
			return true
		}
	}
	return false
}
