package relay

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/protocol_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

const ErrorCodeProtocolCapability types.ErrorCode = "unsupported_protocol_capability"

// PreparedProtocolRequest contains no credentials. Preparation runs before an
// upstream attempt is counted, so incompatible endpoints never consume retries.
type PreparedProtocolRequest struct {
	Endpoint dto.ProtocolEndpoint
	Body     []byte
	adaptor  channel.Adaptor
}

// PrepareProtocolRequest is opt-in. Unconfigured channels keep their native
// adapter behavior. The original client DTO/body remain reusable across retries.
func PrepareProtocolRequest(c *gin.Context, info *relaycommon.RelayInfo) (*PreparedProtocolRequest, *types.NewAPIError) {
	c.Set("protocol_route", nil)
	settings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	policy := settings.ProtocolRouting
	if policy == nil || !policy.Enabled {
		return nil, nil
	}
	if !dto.IsProtocolRoutingFormat(info.RelayFormat) || info.RelayMode == relayconstant.RelayModeCompletions {
		return nil, nil
	}
	capabilityError := func(message string) (*PreparedProtocolRequest, *types.NewAPIError) {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("%s", message), ErrorCodeProtocolCapability, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.InitChannelMeta(c)
	info.ShouldIncludeUsage = true
	if client, ok := info.Request.(*dto.GeneralOpenAIRequest); ok && client.StreamOptions != nil {
		info.ShouldIncludeUsage = client.StreamOptions.IncludeUsage
	}
	if !common.SupportsProtocolRoutingChannelType(info.ChannelType) {
		return capabilityError("channel authentication adapter has not been verified for protocol routing")
	}
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || settings.PassThroughBodyEnabled {
		return capabilityError("protocol routing cannot use pass-through request bodies")
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	raw, err := storage.Bytes()
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	var source map[string]any
	if common.Unmarshal(raw, &source) != nil {
		return capabilityError("cannot inspect protocol request")
	}
	request, err := cloneProtocolRequest(info.Request)
	if err != nil {
		return capabilityError("cannot prepare protocol request")
	}
	if err = helper.ModelMappedHelper(c, info, request); err != nil {
		return nil, newConvertRequestFailedError(c, info, err)
	}
	if err = helper.ApplyReasoningModelSuffix(c, info, request); err != nil {
		return nil, newConvertRequestFailedError(c, info, err)
	}
	if err = protocol_setting.Get().ValidateProduct(policy, info.ChannelType, info.ChannelBaseUrl); err != nil {
		return capabilityError(err.Error())
	}
	source["model"] = info.UpstreamModelName
	candidates, err := service.BuildProtocolCandidates(policy, info.UpstreamModelName, info.RelayFormat, source)
	if err != nil {
		return capabilityError(err.Error())
	}
	original := GetAdaptor(info.ApiType)
	if original == nil {
		return capabilityError("channel adapter is unavailable")
	}
	original.Init(info)
	rejectionReason := "no verified endpoint can preserve the requested protocol capabilities"
	for _, candidate := range candidates {
		copyRequest, copyErr := cloneProtocolRequest(request)
		if copyErr != nil {
			return capabilityError("cannot copy protocol request")
		}
		if err := applyProtocolSystemPrompt(c, info, copyRequest); err != nil {
			return capabilityError("cannot preserve configured system prompt")
		}
		if err := service.ValidateProtocolConversion(info.RelayFormat, candidate.Endpoint.Format, source, candidate.LossPolicy, candidate.Endpoint.Features...); err != nil {
			rejectionReason = err.Error()
			continue
		}
		options := info.ConvOptions()
		options.ToolLossPolicy = types.ConversionLossPolicy(candidate.LossPolicy)
		options.ResponsesLiteBridgeEnabled = false
		options.ResponsesLiteBridge = nil
		for _, feature := range candidate.Endpoint.Features {
			if feature == "responses_lite_bridge" {
				options.ResponsesLiteBridgeEnabled = true
				break
			}
		}
		result, conversionErr := service.ConvertRequest(c, info, candidate.Endpoint.Format, copyRequest)
		if conversionErr != nil {
			continue
		}
		var shaped any
		switch value := result.Value.(type) {
		case *dto.GeneralOpenAIRequest:
			requestedOptions := value.StreamOptions
			adapter := &openai.Adaptor{}
			adapter.Init(info)
			shaped, conversionErr = adapter.ConvertOpenAIRequest(c, info, value)
			if chat, ok := shaped.(*dto.GeneralOpenAIRequest); ok {
				if !info.SupportStreamOptions || !info.IsStream {
					chat.StreamOptions = nil
				} else if constant.ForceStreamOption {
					chat.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
				} else {
					chat.StreamOptions = requestedOptions
				}
			}
		case *dto.ClaudeRequest:
			adapter := &claude.Adaptor{}
			adapter.Init(info)
			shaped, conversionErr = adapter.ConvertClaudeRequest(c, info, value)
		case *dto.OpenAIResponsesRequest:
			adapter := &openai.Adaptor{}
			adapter.Init(info)
			shaped, conversionErr = adapter.ConvertOpenAIResponsesRequest(c, info, *value)
		default:
			continue
		}
		if conversionErr != nil {
			continue
		}
		data, marshalErr := common.Marshal(shaped)
		if marshalErr != nil {
			return capabilityError("cannot encode converted request")
		}
		if candidate.Endpoint.Format == info.RelayFormat {
			var shapedBody map[string]any
			if common.Unmarshal(data, &shapedBody) != nil {
				return capabilityError("cannot inspect native request")
			}
			nativeBody := make(map[string]any, len(source)+len(shapedBody))
			maps.Copy(nativeBody, source)
			maps.Copy(nativeBody, shapedBody)
			data, marshalErr = common.Marshal(nativeBody)
			if marshalErr != nil {
				return capabilityError("cannot encode native request")
			}
		}
		data, err = relaycommon.RemoveDisabledFields(data, info.ChannelOtherSettings, false)
		if err != nil {
			return capabilityError("cannot apply protocol field policy")
		}
		if len(info.ParamOverride) > 0 {
			data, err = relaycommon.ApplyParamOverrideWithRelayInfo(data, info)
			if err != nil {
				return nil, newAPIErrorFromParamOverride(err)
			}
		}
		var outbound map[string]any
		if common.Unmarshal(data, &outbound) != nil {
			return capabilityError("cannot inspect converted request")
		}
		var endpointErr error
		if candidate.Endpoint.Format == info.RelayFormat {
			endpointErr = service.ValidateNativeProtocolEndpointFeatures(candidate.Endpoint, candidate.Endpoint.Format, outbound)
		} else {
			endpointErr = service.ValidateProtocolEndpointFeatures(candidate.Endpoint, candidate.Endpoint.Format, outbound)
		}
		if endpointErr != nil {
			continue
		}
		// The administrator's existing explicit model map is authoritative. Parameter
		// overrides may not silently change the selected model under a capability plan.
		streaming, _ := outbound["stream"].(bool)
		if streaming != info.IsStream {
			return capabilityError("protocol parameter override cannot change client stream mode")
		}
		if outbound["model"] != info.UpstreamModelName {
			return capabilityError("protocol parameter override cannot change the mapped model")
		}
		relaycommon.AppendRequestConversionFromRequest(info, shaped)
		info.FinalRequestRelayFormat = candidate.Endpoint.Format
		c.Set("protocol_route", map[string]any{"source": info.RelayFormat, "target": candidate.Endpoint.Format, "path": candidate.Endpoint.Path, "loss_policy": candidate.LossPolicy, "verified_at": candidate.Endpoint.VerifiedAt})
		return &PreparedProtocolRequest{Endpoint: candidate.Endpoint, Body: data, adaptor: original}, nil
	}
	return capabilityError(rejectionReason)
}

func cloneProtocolRequest(request dto.Request) (dto.Request, error) {
	switch value := request.(type) {
	case *dto.GeneralOpenAIRequest:
		return common.DeepCopy(value)
	case *dto.ClaudeRequest:
		return common.DeepCopy(value)
	case *dto.OpenAIResponsesRequest:
		return common.DeepCopy(value)
	default:
		return nil, fmt.Errorf("unsupported text request")
	}
}

// protocolEndpointAdaptor keeps the channel's existing credential/header setup
// while selecting an administrator-validated path on the same provider host.
type protocolEndpointAdaptor struct {
	channel.Adaptor
	endpoint dto.ProtocolEndpoint
}

func (a *protocolEndpointAdaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	base := info.ChannelBaseUrl
	if special, ok := constant.ChannelSpecialBases[base]; ok {
		if a.endpoint.Format == types.RelayFormatClaude {
			base = special.ClaudeBaseURL
		} else {
			base = special.OpenAIBaseURL
		}
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid protocol upstream base URL")
	}
	return strings.TrimRight(base, "/") + a.endpoint.Path, nil
}
func (a *protocolEndpointAdaptor) SetupRequestHeader(c *gin.Context, headers *http.Header, info *relaycommon.RelayInfo) error {
	if err := a.Adaptor.SetupRequestHeader(c, headers, info); err != nil {
		return err
	}
	if a.endpoint.Format == types.RelayFormatClaude && headers.Get("anthropic-version") == "" {
		headers.Set("anthropic-version", "2023-06-01")
	}
	return nil
}

func ExecuteProtocolRequest(c *gin.Context, info *relaycommon.RelayInfo, plan *PreparedProtocolRequest) *types.NewAPIError {
	usage, apiErr := sendProtocolRequest(c, info, plan)
	if apiErr != nil && (usage == nil || !c.Writer.Written()) {
		return apiErr
	}
	if apiErr != nil && info.StreamStatus != nil {
		info.StreamStatus.RecordError("protocol stream failed")
	}
	audioTokens := usage != nil && (usage.CompletionTokenDetails.AudioTokens > 0 || usage.PromptTokensDetails.AudioTokens > 0)
	audioRatios := ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)
	if (audioTokens && audioRatios) || (info.RelayFormat == types.RelayFormatOpenAIResponses && strings.HasPrefix(info.OriginModelName, "gpt-4o-audio")) {
		service.PostAudioConsumeQuota(c, info, usage, "")
	} else {
		service.PostTextConsumeQuota(c, info, usage, nil)
	}
	return apiErr
}

func sendProtocolRequest(c *gin.Context, info *relaycommon.RelayInfo, plan *PreparedProtocolRequest) (*dto.Usage, *types.NewAPIError) {
	adaptor := &protocolEndpointAdaptor{Adaptor: plan.adaptor, endpoint: plan.Endpoint}
	savedMode, savedPath := info.RelayMode, info.RequestURLPath
	defer func() { info.RelayMode = savedMode; info.RequestURLPath = savedPath }()
	info.RequestURLPath = plan.Endpoint.Path
	switch plan.Endpoint.Format {
	case types.RelayFormatOpenAI:
		info.RelayMode = relayconstant.RelayModeChatCompletions
	case types.RelayFormatClaude:
		info.RelayMode = relayconstant.RelayModeChatCompletions
	case types.RelayFormatOpenAIResponses:
		info.RelayMode = relayconstant.RelayModeResponses
	}
	maxAttempts := 1
	if plan.Endpoint.Format == types.RelayFormatClaude && info.IsStream {
		maxAttempts = 2
	}
	for attempt := range maxAttempts {
		usage, apiErr := sendProtocolRequestAttempt(c, info, plan, adaptor)
		if attempt == 0 && apiErr != nil && info.ReceivedResponseCount == 0 && !c.Writer.Written() && c.Request.Context().Err() == nil && info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonEOF {
			logger.LogWarn(c, "empty native Claude stream; retrying the same endpoint once")
			info.StreamStatus = nil
			service.ResetClaudeWebSearchBilling(c)
			continue
		}
		return usage, apiErr
	}
	return nil, types.NewError(fmt.Errorf("protocol request attempts exhausted"), types.ErrorCodeBadResponse)
}

func sendProtocolRequestAttempt(c *gin.Context, info *relaycommon.RelayInfo, plan *PreparedProtocolRequest, adaptor *protocolEndpointAdaptor) (*dto.Usage, *types.NewAPIError) {
	body, closer, err := relaycommon.NewOutboundJSONBody(plan.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()
	resp, err := channel.DoApiRequest(adaptor, c, info, body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusBadGateway)
	}
	if resp == nil {
		return nil, types.NewError(fmt.Errorf("empty upstream response"), types.ErrorCodeBadResponse)
	}
	if resp.StatusCode != http.StatusOK {
		apiErr := service.RelayErrorHandler(c.Request.Context(), resp, false)
		service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
		return nil, apiErr
	}
	upstreamStream := isResponsesEventStreamContentType(resp.Header.Get("Content-Type"))
	if upstreamStream != info.IsStream {
		service.CloseResponseBodyGracefully(resp)
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("upstream stream mode does not match the requested protocol"), types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	switch plan.Endpoint.Format {
	case types.RelayFormatClaude:
		usage, apiErr := (&claude.Adaptor{}).DoResponse(c, resp, info)
		normalized, _ := usage.(*dto.Usage)
		return normalized, apiErr
	case types.RelayFormatOpenAI:
		if info.RelayFormat == types.RelayFormatOpenAIResponses {
			if info.IsStream {
				return openai.OaiChatToResponsesStreamHandler(c, info, resp)
			}
			return openai.OaiChatToResponsesHandler(c, info, resp)
		}
		adapter := &openai.Adaptor{}
		adapter.Init(info)
		usage, apiErr := adapter.DoResponse(c, resp, info)
		normalized, _ := usage.(*dto.Usage)
		return normalized, apiErr
	default:
		if info.RelayFormat != types.RelayFormatOpenAIResponses {
			if info.IsStream {
				return openai.OaiResponsesToChatStreamHandler(c, info, resp)
			}
			return openai.OaiResponsesToChatHandler(c, info, resp)
		}
		if info.IsStream {
			return openai.OaiResponsesStreamHandler(c, info, resp)
		}
		return openai.OaiResponsesHandler(c, info, resp)
	}
}

func applyClaudeSystemPrompt(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) {
	if info.ChannelSetting.SystemPrompt != "" {
		if request.System == nil {
			request.SetStringSystem(info.ChannelSetting.SystemPrompt)
		} else if info.ChannelSetting.SystemPromptOverride {
			common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
			if request.IsStringSystem() {
				existing := strings.TrimSpace(request.GetStringSystem())
				if existing == "" {
					request.SetStringSystem(info.ChannelSetting.SystemPrompt)
				} else {
					request.SetStringSystem(info.ChannelSetting.SystemPrompt + "\n" + existing)
				}
			} else {
				systemContents := request.ParseSystem()
				newSystem := dto.ClaudeMediaMessage{Type: dto.ContentTypeText}
				newSystem.SetText(info.ChannelSetting.SystemPrompt)
				if len(systemContents) == 0 {
					request.System = []dto.ClaudeMediaMessage{newSystem}
				} else {
					request.System = append([]dto.ClaudeMediaMessage{newSystem}, systemContents...)
				}
			}
		}
	}

}

func applyProtocolSystemPrompt(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	switch value := request.(type) {
	case *dto.GeneralOpenAIRequest:
		applySystemPromptIfNeeded(c, info, value)
	case *dto.ClaudeRequest:
		applyClaudeSystemPrompt(c, info, value)
	case *dto.OpenAIResponsesRequest:
		if info.ChannelSetting.SystemPrompt == "" {
			return nil
		}
		var original string
		if len(value.Instructions) > 0 && common.Unmarshal(value.Instructions, &original) != nil {
			return fmt.Errorf("instructions must be text")
		}
		if original != "" && !info.ChannelSetting.SystemPromptOverride {
			return nil
		}
		prompt := info.ChannelSetting.SystemPrompt
		if original != "" {
			prompt += "\n" + original
			common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
		}
		encoded, err := common.Marshal(prompt)
		if err != nil {
			return err
		}
		value.Instructions = encoded
	}
	return nil
}
