package relay

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// RunProtocolProbe uses the ordinary adapter authentication, header overrides,
// proxy/transport policy and redirect refusal. It never enters quota settlement
// or channel health/retry paths. Each case is exactly one bounded upstream call.
func RunProtocolProbe(ctx context.Context, upstream *model.Channel, test model.ProtocolProbeCase, maxTokens int) model.ProtocolProbeResult {
	started := time.Now()
	result := model.ProtocolProbeResult{Outcome: "unknown", Reason: "configuration_unavailable"}
	if !common.SupportsProtocolRoutingChannelType(upstream.Type) {
		return result
	}
	var settings dto.ChannelSettings
	if upstream.Setting != nil && *upstream.Setting != "" && common.UnmarshalJsonStr(*upstream.Setting, &settings) != nil {
		return result
	}
	if settings.ValidateHTTPTransport() != nil {
		return result
	}
	if _, err := common.ParseProxyURLStrict(settings.Proxy); err != nil {
		return result
	}
	if (dto.ProtocolModelPolicy{EntryFormats: []types.RelayFormat{test.Endpoint.Format}, Endpoints: []dto.ProtocolEndpoint{test.Endpoint}}).Validate() != nil {
		return result
	}
	body, stream, err := service.BuildProtocolProbeFixture(test.Endpoint.Format, test.Model, test.Check, maxTokens)
	if err != nil {
		return result
	}
	// A multikey channel is one diagnostic account sample, chosen deterministically
	// without advancing its polling cursor or mutating health state.
	key := upstream.Key
	if upstream.ChannelInfo.IsMultiKey {
		key = ""
		for i, candidate := range upstream.GetKeys() {
			status, exists := upstream.ChannelInfo.MultiKeyStatusList[i]
			if !exists || status == common.ChannelStatusEnabled {
				key = candidate
				break
			}
		}
	}
	if key == "" {
		result.Reason = "no_eligible_key"
		return result
	}
	apiType, ok := common.ChannelType2APIType(upstream.Type)
	if !ok {
		return result
	}
	adaptor := GetAdaptor(apiType)
	if adaptor == nil {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, test.Endpoint.Path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: upstream.Id, ChannelType: upstream.Type, ChannelBaseUrl: upstream.GetBaseURL(), ApiType: apiType, ApiKey: key, UpstreamModelName: test.Model, ChannelSetting: settings, HeadersOverride: upstream.GetHeaderOverride(), ChannelOtherSettings: upstream.GetOtherSettings()}, OriginModelName: test.Model, RelayFormat: test.Endpoint.Format, FinalRequestRelayFormat: test.Endpoint.Format, RequestURLPath: test.Endpoint.Path, IsStream: stream, DisablePing: true, IsChannelTest: true, StartTime: started, RelayMode: relayconstant.RelayModeChatCompletions}
	if upstream.OpenAIOrganization != nil {
		info.Organization = *upstream.OpenAIOrganization
	}
	if test.Endpoint.Format == types.RelayFormatOpenAIResponses {
		info.RelayMode = relayconstant.RelayModeResponses
	}
	adaptor.Init(info)
	response, err := channel.DoApiRequest(&protocolEndpointAdaptor{Adaptor: adaptor, endpoint: test.Endpoint}, c, info, bytes.NewReader(body))
	if err != nil {
		result.Reason = "transport_error"
		if ctx.Err() == context.Canceled {
			result.Outcome = "cancelled"
			result.Reason = "cancelled"
		} else if ctx.Err() == context.DeadlineExceeded {
			result.Reason = "timeout"
		}
		result.ElapsedMS = time.Since(started).Milliseconds()
		return result
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, service.ProtocolProbeResponseLimit+1))
	if err != nil {
		result.HTTPStatus = response.StatusCode
		result.Reason = "response_interrupted"
		if ctx.Err() == context.Canceled {
			result.Outcome = "cancelled"
			result.Reason = "cancelled"
		} else if ctx.Err() == context.DeadlineExceeded {
			result.Reason = "timeout"
		}
	} else {
		result = service.EvaluateProtocolProbe(test.Endpoint.Format, test.Check, response.StatusCode, data)
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	return result
}
