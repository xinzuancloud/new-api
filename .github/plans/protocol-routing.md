# Model-aware protocol routing implementation plan

> Execute with superpowers:subagent-driven-development; keep changes isolated in codex/protocol-routing. User approved the design in this conversation on 2026-09-08.

Goal: accept stateless Chat Completions, Anthropic Messages and OpenAI Responses through validated model/endpoint paths while preserving group/model authorization, supplier order, exhaustive account traversal, billing and stream integrity.

Architecture: opt-in typed configuration in the existing ChannelSettings JSON; model override replaces the channel default policy. A service planner detects required features and selects compatible declared endpoints. A relay bridge reuses existing RelayKit and native response handlers; controller rejects incompatible candidates before counting an upstream attempt. Account resource IDs share cooldown and exclusion across channel aliases. Existing unconfigured channels retain legacy behavior.

Scope: no generic response history store, provider-hosted state emulation, count_tokens endpoint or automatic provider probes. No credential duplication. No source model substitution. No request content in diagnostics. No new files below docs/. Preserve upstream release-driven main policy and existing custom functionality.

## Configuration contract

ChannelSettings.protocol_routing optional object:
- enabled bool
- account_resource string (administrator nonsecret identity, optional; absent uses existing channel identity)
- quota_scope: model (default) or account
- defaults: ProtocolModelPolicy
- models: map keyed by mapped upstream model; complete per-model policy override
ProtocolModelPolicy: entry_formats (array of relay format constants), endpoints (ProtocolEndpoint array), loss_policy (safe default; allow/strict supported).
ProtocolEndpoint: format (openai/claude/openai-responses actual constants), path (absolute path on existing provider host; no URL/authority/query/fragment), features (array), verified bool, verified_at optional RFC3339 timestamp.
Features: stream, tools, parallel_tools, images, files, audio, video, structured_output, reasoning, hosted_tools, stateful, background. Ordinary text requires no feature. Unverified paths are ineligible. Phase-one cross-format stateful/background paths rejected. Native stateful unsupported unless explicitly declared and existing native provider path handles it; never emulate stored history.

## Tasks
- [ ] Reconcile deployed custom code with fork main and exact latest upstream release tag in isolated branch; preserve custom cache display and policy translations. Establish baseline checks.
- [ ] DTO config validation and capability planner. Files: relaykit/dto/channel_settings.go, relaykit/dto/protocol_routing.go, service/protocol_policy.go, service/protocol_policy_test.go. API: BuildProtocolCandidates(policy *dto.ProtocolRoutingSettings, upstreamModel string, source types.RelayFormat, request any) ([]ProtocolCandidate,error); candidate has Endpoint dto.ProtocolEndpoint and LossPolicy string. Validate bounded lists, safe relative endpoint, known enum/features, explicit entries, model overrides. Tests reject unsupported/stateful conversions and preserve native preference and feature requirements.
- [ ] Share account cooldown/exclusion. Files: service/routing_policy.go and existing service/routing_policy_test.go. Account identity independent of endpoint/channel aliases; quota scope configurable; business supplier order unchanged. Test alias cooldown, distinct-account independence, exhaustive traversal and pin semantics.
- [ ] Relay bridge and preflight routing. Files: relay/protocol_routing.go, relay/protocol_routing_test.go, controller/relay.go, service/log_info_generate.go, relay/common/relay_info.go as needed. API: PrepareProtocolRequest(c,info,channel) returns prepared plan/error before addUsedChannel; ExecuteProtocolRequest reuses existing handlers. Preserve original DTO across attempts; validate mapped model/parameters; original adaptor supplies auth, explicit endpoint determines URL, target wire adaptor supplies protocol shaping. Preflight skipped candidates do not consume total attempt budget. Unsupported capability returns clear 400, exhausted health returns 503. Existing response handlers preserve SSE terminal errors and usage. No retry after downstream bytes.
- [ ] Channel UI configuration. Existing channel settings form with structured defaults/model overrides and shared components, JSON detail editor if complex rows; no duplicate channel-per-format pattern. 7 locale translations via prescribed i18n tool. Validate save/load and safe defaults using feature tests. Integrate server validation in existing channel model normalization.
- [ ] Verification: root focused then full tests/vet; independent relaykit build/tests; frontend typecheck/lint/focused tests/build; actual SQLite/MySQL/PostgreSQL channel-setting persistence and routing matrix. Full application against local simulated upstreams covers six directions, SSE/function round-trip, unsupported features/stateful, supplier order and shared account cooldown.
- [ ] Independent scoped and final review; fix findings; deploy versioned image with metadata-only rollback snapshot. Configure verified initial capabilities for current models, leave unknown features unavailable. Preserve prices/group permissions/CTYun state. Passive verification only unless a bounded provider test is explicitly needed and already authorized. Update existing runbook/verification/monitor to include conversion paths and skips. Verify deployed HTTP version/health and real traffic; no completion claim with required checks pending.

## Baseline and decisions
- Existing upstream rc.34 has 3 unrelated frontend failures already confirmed before this change. Do not mask or weaken them; report their status separately.
- Merge work uses existing .worktrees directory; root main remains untouched during implementation.
- Work is scoped to stateless protocol compatibility. Persisted server-side conversation emulation is a separate subsystem deliberately excluded by the approved design.
