package dto

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/relaykit/types"
)

// ProtocolRoutingSettings declares administrator-verified endpoints on this
// channel's existing upstream host. Model policies replace defaults completely.
type ProtocolRoutingSettings struct {
	Enabled         bool                           `json:"enabled"`
	AccountResource string                         `json:"account_resource,omitempty"`
	QuotaScope      string                         `json:"quota_scope,omitempty"`
	Defaults        ProtocolModelPolicy            `json:"defaults"`
	Models          map[string]ProtocolModelPolicy `json:"models,omitempty"`
}

type ProtocolModelPolicy struct {
	EntryFormats []types.RelayFormat `json:"entry_formats"`
	Endpoints    []ProtocolEndpoint  `json:"endpoints"`
	LossPolicy   string              `json:"loss_policy,omitempty"`
}

type ProtocolEndpoint struct {
	Format     types.RelayFormat `json:"format"`
	Path       string            `json:"path"`
	Features   []string          `json:"features,omitempty"`
	Verified   bool              `json:"verified"`
	VerifiedAt string            `json:"verified_at,omitempty"`
}

func (s *ProtocolRoutingSettings) Validate() error {
	if s == nil {
		return nil
	}
	if len(s.AccountResource) > 128 || strings.TrimSpace(s.AccountResource) != s.AccountResource || strings.IndexFunc(s.AccountResource, unicode.IsControl) >= 0 {
		return fmt.Errorf("protocol_routing.account_resource must be a nonsecret identifier of at most 128 bytes")
	}
	if s.QuotaScope != "" && s.QuotaScope != "model" && s.QuotaScope != "account" {
		return fmt.Errorf("protocol_routing.quota_scope must be model or account")
	}
	if len(s.Models) > 256 {
		return fmt.Errorf("protocol_routing supports at most 256 model overrides")
	}
	if s.Enabled || len(s.Defaults.EntryFormats) > 0 || len(s.Defaults.Endpoints) > 0 || s.Defaults.LossPolicy != "" {
		if err := s.Defaults.Validate(); err != nil {
			return fmt.Errorf("protocol_routing.defaults: %w", err)
		}
	}
	for name, policy := range s.Models {
		if len(name) == 0 || len(name) > 256 || strings.TrimSpace(name) != name || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return fmt.Errorf("protocol_routing model identifier is invalid")
		}
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("protocol_routing model override: %w", err)
		}
	}
	return nil
}

func (p ProtocolModelPolicy) Validate() error {
	switch p.LossPolicy {
	case "", "allow", "safe", "strict":
	default:
		return fmt.Errorf("invalid loss_policy")
	}
	if len(p.EntryFormats) == 0 || len(p.EntryFormats) > 3 {
		return fmt.Errorf("entry_formats requires 1 to 3 formats")
	}
	entries := map[types.RelayFormat]bool{}
	for _, format := range p.EntryFormats {
		if !IsProtocolRoutingFormat(format) || entries[format] {
			return fmt.Errorf("entry_formats contains an unknown or duplicate format")
		}
		entries[format] = true
	}
	if len(p.Endpoints) == 0 || len(p.Endpoints) > 16 {
		return fmt.Errorf("endpoints requires 1 to 16 paths")
	}
	seen := map[string]bool{}
	for _, endpoint := range p.Endpoints {
		if !IsProtocolRoutingFormat(endpoint.Format) {
			return fmt.Errorf("endpoint format is invalid")
		}
		u, err := url.Parse(endpoint.Path)
		if err != nil || len(endpoint.Path) > 1024 || !strings.HasPrefix(endpoint.Path, "/") || strings.HasPrefix(endpoint.Path, "//") || strings.ContainsAny(endpoint.Path, "\\?#%") || strings.IndexFunc(endpoint.Path, unicode.IsSpace) >= 0 || strings.IndexFunc(endpoint.Path, unicode.IsControl) >= 0 || u.IsAbs() || u.Host != "" || path.Clean(endpoint.Path) != endpoint.Path {
			return fmt.Errorf("endpoint path must be an absolute path on the existing provider host without query, fragment or traversal")
		}
		key := string(endpoint.Format) + "\n" + endpoint.Path
		if seen[key] {
			return fmt.Errorf("duplicate endpoint")
		}
		seen[key] = true
		if len(endpoint.Features) > 13 {
			return fmt.Errorf("endpoint has too many features")
		}
		features := map[string]bool{}
		for _, feature := range endpoint.Features {
			switch feature {
			case "stream", "tools", "parallel_tools", "images", "files", "audio", "video", "structured_output", "reasoning", "hosted_tools", "context_editing", "stateful", "background":
			default:
				return fmt.Errorf("endpoint feature is invalid")
			}
			if features[feature] {
				return fmt.Errorf("duplicate endpoint feature")
			}
			features[feature] = true
		}
		if endpoint.VerifiedAt != "" {
			if _, err := time.Parse(time.RFC3339, endpoint.VerifiedAt); err != nil {
				return fmt.Errorf("verified_at must be an RFC3339 timestamp")
			}
		}
	}
	return nil
}

func IsProtocolRoutingFormat(format types.RelayFormat) bool {
	return format == types.RelayFormatOpenAI || format == types.RelayFormatClaude || format == types.RelayFormatOpenAIResponses
}
