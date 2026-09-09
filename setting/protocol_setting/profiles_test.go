package protocol_setting

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestResolution(t *testing.T) {
	catalog, err := Parse(`{"vendor":{"name":"Vendor","channel_types":[1],"base_urls":["https://example.com"],"defaults":{"entry_formats":["openai"],"endpoints":[{"format":"openai","path":"/v1/chat/completions","features":["tools","stream"],"unsupported_features":["images"],"verified":true}],"loss_policy":"strict"},"models":{"special":{"endpoint_overrides":[{"format":"openai","features":{"tools":false},"unsupported_features":{"images":false,"hosted_tools":true}}]}}}}`)
	require.NoError(t, err)
	settings := &dto.ProtocolRoutingSettings{Enabled: true, Profile: "vendor"}
	require.NoError(t, catalog.ValidateChannel(settings, 1, "https://example.com/"))
	require.Error(t, catalog.ValidateChannel(settings, 1, "https://other.example"))
	policy, err := catalog.Resolve(settings, "special")
	require.NoError(t, err)
	assert.Equal(t, []string{"stream"}, policy.Endpoints[0].Features)
	assert.Equal(t, []string{"hosted_tools"}, policy.Endpoints[0].UnsupportedFeatures)
	plain, err := catalog.Resolve(settings, "plain")
	require.NoError(t, err)
	assert.Equal(t, []string{"tools", "stream"}, plain.Endpoints[0].Features)
	assert.Equal(t, []string{"images"}, plain.Endpoints[0].UnsupportedFeatures)
	replacement := dto.ProtocolModelPolicy{EntryFormats: []types.RelayFormat{types.RelayFormatOpenAI}, Endpoints: plain.Endpoints}
	settings.Models = map[string]dto.ProtocolModelPolicy{"special": replacement}
	policy, err = catalog.Resolve(settings, "special")
	require.NoError(t, err)
	assert.Empty(t, policy.LossPolicy)
	settings.Models = map[string]dto.ProtocolModelPolicy{"special": {EndpointOverrides: []dto.ProtocolEndpointOverride{{Format: types.RelayFormatClaude}}}}
	_, err = catalog.Resolve(settings, "special")
	require.Error(t, err)
	settings.Profile = "missing"
	_, err = catalog.Resolve(settings, "plain")
	require.Error(t, err)
}

func TestProfileSpecialProductAliases(t *testing.T) {
	catalog, err := Parse(`{"kimi":{"name":"Kimi","base_urls":["kimi-coding-plan"],"defaults":{"entry_formats":["openai"],"endpoints":[{"format":"openai","path":"/v1/chat/completions"}]}}}`)
	require.NoError(t, err)
	settings := &dto.ProtocolRoutingSettings{Enabled: true, Profile: "kimi"}
	require.NoError(t, catalog.ValidateChannel(settings, 25, "kimi-coding-plan"))
	require.NoError(t, catalog.ValidateChannel(settings, 25, "https://api.kimi.com/coding/v1"))
	require.Error(t, catalog.ValidateChannel(settings, 25, "https://api.kimi.com/v1"))
	profile := catalog["kimi"]
	profile.BaseURLs = []string{"unknown-plan"}
	catalog["kimi"] = profile
	_, err = Parse(JSON(catalog))
	require.Error(t, err)
}

func TestPartialEndpointOverridesValidateAndStayIsolated(t *testing.T) {
	catalog, err := Parse(`{"vendor":{"name":"Vendor","defaults":{"entry_formats":["openai"],"endpoints":[{"format":"openai","path":"/a","features":["tools"],"verified":true},{"format":"openai","path":"/b","verified":true}]}}}`)
	require.NoError(t, err)
	yes := true
	no := false
	empty := ""
	for _, tc := range []struct {
		name      string
		override  dto.ProtocolModelPolicy
		wantError bool
	}{
		{"ambiguous", dto.ProtocolModelPolicy{EndpointOverrides: []dto.ProtocolEndpointOverride{{Format: types.RelayFormatOpenAI, Verified: &yes}}}, true},
		{"dangling", dto.ProtocolModelPolicy{EndpointOverrides: []dto.ProtocolEndpointOverride{{Format: types.RelayFormatClaude, Path: "/a"}}}, true},
		{"unknown feature removal", dto.ProtocolModelPolicy{EndpointOverrides: []dto.ProtocolEndpointOverride{{Format: types.RelayFormatOpenAI, Path: "/a", Features: map[string]bool{"future": false}}}}, true},
		{"empty formats", dto.ProtocolModelPolicy{EntryFormats: []types.RelayFormat{}}, true},
		{"invalid loss", dto.ProtocolModelPolicy{LossPolicy: "unknown"}, true},
		{"false and timestamp clear", dto.ProtocolModelPolicy{EndpointOverrides: []dto.ProtocolEndpointOverride{{Format: types.RelayFormatOpenAI, Path: "/a", Verified: &no, VerifiedAt: &empty, Features: map[string]bool{"tools": false, "images": true}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := &dto.ProtocolRoutingSettings{Enabled: true, Profile: "vendor", Models: map[string]dto.ProtocolModelPolicy{"specific": tc.override}}
			err := catalog.ValidateChannel(settings, 1, "https://example.com")
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			specific, err := catalog.Resolve(settings, "specific")
			require.NoError(t, err)
			assert.False(t, specific.Endpoints[0].Verified)
			assert.Equal(t, []string{"images"}, specific.Endpoints[0].Features)
			other, err := catalog.Resolve(settings, "other")
			require.NoError(t, err)
			assert.True(t, other.Endpoints[0].Verified)
			assert.Equal(t, []string{"tools"}, other.Endpoints[0].Features)
		})
	}
}
