# Team routing verification

Current runtime: `v1.0.0-rc.35-fork.20260908.t153045.g02312508e398`. Historical rc.33 verification is retained below; the latest deployment is recorded in the 2026-09-08 sections.

Code commit: `2c40be03`. Custom version: `v1.0.0-rc.33-fork.20260906.t143023.g2c40be03`.

## Automated checks

- `GOWORK=off go test ./...`: all root-module packages passed.
- After final controller lifecycle/status changes: `go test ./controller ./service ./middleware ./relay/helper ./pkg/perf_metrics -count=1` and focused controller regressions passed.
- `GOWORK=off go vet ./controller ./service ./model ./middleware ./relay/helper ./pkg/perf_metrics`: passed.
- `cd relaykit && GOWORK=off go build ./...`: passed independently.
- `bun run typecheck`: passed.
- `bun run test src/features/system-settings/models/__tests__ src/features/pricing/__tests__`: 9 files, 78 tests passed.
- Affected frontend oxlint/format, seven-locale sync/completeness: passed.
- Versioned frontend build and Linux amd64 CGO-disabled build: passed.

## Real database matrix

Routing tests initialize actual database state, then exercise memory-cache on/off in each engine. No ORM/driver/schema/migration changes; comparison to production rc.32 found no changes in `model/`, `go.mod`, `go.sum` before this feature.

| Engine | Version | Command | Result |
|---|---|---|---|
| SQLite through project Go driver | 3.50.4 | `go test ./service -run TestRoutingPolicy -count=1` | Passed |
| PostgreSQL | 15.19 | `NEW_API_ROUTING_TEST_DSN='postgres://postgres:local-routing-test@127.0.0.1:25432/routing_test?sslmode=disable' go test ./service -run TestRoutingPolicy -count=1` | Passed |
| MySQL | 8.0.46 | `NEW_API_ROUTING_TEST_DSN='root:local-routing-test@tcp(127.0.0.1:23306)/routing_test?charset=utf8mb4&parseTime=True&loc=Local' go test ./service -run TestRoutingPolicy -count=1` | Passed |

Test fixture uses rollback transactions and has no production credentials. Exact local endpoint ports: PostgreSQL 25432, MySQL 23306 on loopback. Test-only credentials were synthetic. No minimum-version-specific feature was introduced.

## Full application simulation

Final committed application ran with a disposable SQLite database, synthetic users/tokens and a local HTTP provider simulator; no real model API was called. All 15 cases passed:

- Default light-model access and rejection of K3.
- Plus free flagship access, rejection of unapproved GLM flagship, Fire/256K then Kimi/256K fallback.
- Dev SenseNova → Fire → Kimi.
- VIP Kimi → Fire → SenseNova → metered, including all eight bounded attempts and K3 aliases.
- Same VIP session returns to Kimi after recovery.
- Plus cannot reach metered when all its eligible channels fail.
- Each request's channel IDs are unique and supplier tier indices never move backward.

Runtime evidence: local `/private/tmp/newapi-team-routing-build/validation-final.json` (synthetic channel IDs only).

## Passive pre-deployment baseline

Observed 2026-09-06 14:13:39 UTC+8, prior 60-minute window ending with a 60-second settlement grace period:

- API POST responses: HTTP 200 = 456, HTTP 429 = 41, status 0 = 1; 498 samples.
- Entry HTTP P95 duration: 16.6809 seconds.
- This is gateway access-log data, not proof all 200 streams completed. Post-deployment comparison must include `stream_status` and sample sizes.

The baseline remains on the host at `/opt/ops-backups/team-routing-candidate/baseline.json`. No production credentials, users, tokens or raw request bodies were copied to the development machine.

## Operational approval boundaries

Automatic review rejected extra full-DB/.env duplication and a production test login role. Neither action ran. Verification moved to local isolated instances; rollout preserves the original runtime image and uses a nonsecret metadata/settings snapshot and a separate Compose image override. CTYun stays disabled until deployment and monitoring verification finish.

## Production rollout

- Configuration applied at 2026-09-06 14:35:47 UTC+8, with CTYun disabled; snapshot `/opt/ops-backups/team-routing/20260906_143547/settings-before.json`.
- Custom application started at `2026-09-06T06:37:22.425812611Z`; container healthy and public/internal `/api/status` both report the release version. Original official image remains available. Runtime override: `/opt/new-api/docker-compose.override.yml`.
- New policy exactly matches approved group order; Default still exposes exactly its two light models; no metered ability belongs to a non-VIP group.
- Filled 13 verified missing cache read discounts. Re-applying the configuration produced no additional price changes, preserving existing overrides.
- Stable two-minute observation at 14:42:28 UTC+8: 12 HTTP200 samples; 13 correlated consumption records, zero error attempts, zero stream-error markers and zero missing stream metadata. Requests used Kimi for VIP. Entry P95 28.18s; sample is too small and differs from the baseline to claim a latency improvement.
- Earlier three-minute window included four 502 responses during single-instance cutover; exclude this transition when assessing steady-state behavior.
- Operations scripts and documentation installed; hourly quiet-unless-actionable Codex heartbeat `new-api` created in this task.
- CTYun enabled only as the final step at 14:44:38 UTC+8, VIP-only; final-step snapshot `/opt/ops-backups/team-routing/20260906_144438/settings-before.json`. No paid generation probe was made.

Final read-only check after cache synchronization: CTYun status=1, its 12 enabled model abilities belong only to VIP; k3 maps to kimi-k3. The latest five-minute window contained 35 K3 consumption records; all 35 explicitly recorded cache_ratio=0.1 and none had a stream-error marker. All four production containers were healthy/running. Local disposable MySQL/PostgreSQL test containers were removed after verification.

Minor UI follow-up: effective default cache prices are marked in table/detail views; the compact card view shows the effective numeric rate without the extra default badge. This does not change charges.

## 2026-09-07 Exhaustive provider traversal

User approved trying all currently eligible provider accounts before fallback, superseding the original three-account cap. Code commit `3f015753`, version `v1.0.0-rc.33-fork.20260907.t100757.g3f015753`. No new documentation files were added.

- Per-tag limit0 means all eligible untried channels; positive limits1–256 remain available for backward compatibility.
- Independent max_total_attempts32 covers the current longest17-channel route. Config value0 inherits native RetryTimes. Exhaustion or the unchanged300s deadline ends the request; all-mode never reserves budget by skipping remaining supplier accounts.
- Auto group counter resets cannot bypass the absolute request limit. Exhaustive mode stays in the current group until its eligible candidates are exhausted; saved finite policies using native retry budgets retain their old cross-group behavior.
- Root `GOWORK=off go test ./...`, focused regressions, changed-package vet and `cd relaykit && GOWORK=off go build ./...` passed.
- Real SQLite3.50.4, PostgreSQL15.19, MySQL8.0.46 routing tests passed with cache on/off using the same commands above and `-run 'TestRoutingPolicy|TestRoutingAttemptLimit'`. Tests cover12 eligible accounts,10 when one is disabled and one cooling, no early fallback at budget exhaustion, and Auto behavior.
- UI typecheck/lint/format and27 focused routing tests passed; all6 new strings translated in7 locales through the prescribed script.
- Complete application with local simulated providers passed17 scenarios. Plus/DeepSeek tried12 distinct SenseNova accounts before Volcengine on attempt13. VIP/K3 tried3 Kimi,1 Volcengine,12 SenseNova, then metered on attempt17. No real model probes or production credential copies were used.
- Artifact evidence: `/private/tmp/newapi-team-routing-build/exhaustive-validation.json`; detailed root test output `/private/tmp/newapi-team-routing-build/exhaustive-go-tests.log`.

Deployment completed 2026-09-07 around10:15 UTC+8. A temporary same-application instance received traffic while the primary was replaced; Caddy was restored to `new-api:3000` and the temporary container removed. Only RoutingPolicy attempt fields changed; hashes of ModelRatio/CompletionRatio/CacheRatio/GroupRatio remained identical. CTYun remained enabled and VIP-only. Rollback snapshot: `/opt/ops-backups/exhaustive-routing-20260907_101524`. No production model probes were made.

Post-rollout checks at2026-09-07 10:20 UTC+8: public/internal versions match `g3f015753`, all production containers healthy, temporary instance removed. DB policy has max_attempts_per_tag0 and max_total_attempts32; Default retains exactly its two models and no metered ability belongs to non-VIP. The latest12 Plus/DeepSeek consumption records all completed on SenseNova with normal stream status; observed attempts1–3 succeeded before exhausting the provider, so they do not themselves prove a12-failure path. That path is verified by the17-scenario simulation. Caddy recorded19 API POST responses after rollout, all HTTP200; no transition5xx observed in that sample. Hourly passive monitoring remains active.


## 2026-09-08 Protocol capability routing verification

Implementation integrates the deployed custom branch with the exact upstream rc.35 release and fork release automation; no upstream/main synchronization or tag overwrite.

- Full disposable application matrix: 185/185 checks passed across all nine native/cross-format combinations, ordinary responses, SSE tools/history/results, explicit upstream errors, truncated EOF, client usage preferences and conditional upstream usage negotiation.
- Routing cases: supplier preference over native-protocol preference; 14 incompatible channels skipped before one real attempt; 12 distinct failures then supplier fallback on attempt13; account-alias cooldown; pins; state/permission rejection; legacy paths; mapped model integrity; configured system prompt preservation.
- Billing evidence:55 settlements,5740 internal quota, exact wallet/token conservation. Claude fixture reports input100 plus cached20, Chat fixture input100 inclusive cached20; both preserve cache20 with their intended differing charges. Failed/preflight requests are not blindly treated as successful or charged. Partial reported usage is settled once; missing upstream usage is explicitly estimated, not asserted as real supplier usage.
- Artifact: `/private/tmp/protocol-e2e/run-20260908-142723/results.json`, reproduction `/private/tmp/protocol-e2e/verify.py`.
- Database configuration persistence and routing passed SQLite3.50.4, PostgreSQL15.18, MySQL8.0.46. Driver upgrade compatibility:9/9 fresh/current, deployed-rc33 upgrade, released-rc35 upgrade cases; real InitDB/InitLogDB, two current startup migrations, seeded data/indexes/constraints/uniqueness preserved, zero second-startup DDL. Main/log stores isolated in migration harness; SQLite log path is switched explicitly for function coverage because application SQLite path is shared. Report `/private/tmp/protocol-migration/REPORT.md`.
- Frontend full suite passed729 tests; typecheck, affected lint, seven-locale synchronization and production build passed. Two remaining CI-only upstream test races were corrected by waiting for loaded query content and actual entrance-animation visibility; Linux CI is rechecked after this test-only change.
- Limited native provider checks: Kimi Chat and Messages, SenseNova Chat and Volcengine Chat produced a streamed function call and terminal response. Kimi named tool selection required thinking disabled; probes did not change deployed settings. These are representative endpoint checks, not an assertion every model/parameter/multimodal combination works. No CTYun paid probe was made.
- Deployment application script passed real PostgreSQL dry-run/apply/idempotent-no-op with quoted resource identifiers; preserves unrelated settings and snapshots only prior protocol configuration.

Production rollout version, hashes and passive verification are recorded after deployment.


### Context-editing hotfix

A user-reported400 exposed overly broad stateful classification on native Messages. Native context editing is now distinct from stored conversation/background state. Exact native Claude/Responses preservation of context_management objects/arrays/empty objects is covered; safe/strict cross-format and Chat paths still reject unsupported preservation. Full disposable application matrix199/199 passed,59 settlements and6188 internal quota conserved; artifact `/private/tmp/protocol-e2e/run-20260908-151956/results.json`. Focused tests and independent RelayKit build passed. Full frontend730 tests passed when run separately from heavy concurrent Go compilation; an unrelated metadata-editing timing failure occurred under simultaneous load and is retained in `/private/tmp/protocol-context-web.log` for transparency.

Native `/v1/messages` plus context_management was verified via bearer authentication on Kimi, SenseNova and Volcengine with small requests; no request content or credentials were logged in the evidence. No CTYun paid probe was made.


### Final production rollout (2026-09-08)

- Runtime `v1.0.0-rc.35-fork.20260908.t153045.g02312508e398`; primary and public `/api/status` agree and all four production containers are healthy/running. Temporary canary removed after the300-second request drain. Original Caddy configuration restored exactly (hash verified).
-39 channel policies enabled, with native Messages endpoints on Kimi/SenseNova/Volcengine and context_editing declared only on native Messages paths. Group/model abilities, input/output/cache/group prices, supplier ordering and CTYun status1 remained identical to pre-rollout guards.
- Initial snapshot `/opt/ops-backups/protocol-routing/20260908T065345250494Z.json`; final re-enable snapshot `/opt/ops-backups/protocol-routing/20260908T075121935451Z.json`. Guards and sanitized probe outcomes live in the same directory.
- Server GHCR pull returned unauthorized. No GitHub login credential was copied. Runtime uses a local image built from the versioned, tested static Linux binary; hotfix binary SHA256 `f4022beff21a92b6ec71e176ee177416f6cc26e05534096800d218526d5f7a29` matched local and server copies. Formal GHCR images/signatures, binaries and Electron releases also completed successfully.
- After re-enabling policies, a management-account Messages request with clear_thinking context_management returned200/end_turn and was logged as Claude→Claude. A Responses request returned200/completed with usage and was logged as Responses→Chat. Both were bounded synthetic checks, not team request bodies.
- CI34199701617, GHCR34199958696, binary release34199962314 and Electron34199965862 succeeded. Frontend730 tests, root/RelayKit tests and independent module build passed; independent review clear. The context-editing regression is covered by199 complete-app checks.
- Remaining upstream limitation observed during final passive checks: SenseNova kimi-k3 produces429 rate limits and sometimes HTTP200 with an empty SSE body. A bounded direct synthetic check confirmed200/text-event-stream with0 body bytes. New routing correctly records incomplete upstream streams as failures rather than complete success. This is separate from the corrected native-context400. No attempts were made to weaken terminal checks, change pricing, widen model permissions or switch CTYun state.
- Stateful stored-history/background emulation and the disabled Messages count_tokens endpoint remain outside this first phase. Unsupported cross-format context_management is rejected explicitly; native Chat does not advertise unsupported forwarding.

### Codex client capability diagnostics (2026-09-08)

User reported the generic capability rejection from a ChatGPT client. Access logs identify the 16:30:56 and 16:31:27 UTC+8 requests as Windows Codex Desktop 0.153.4, `/v1/responses`; matching application request IDs end in `phJRIXG8` and `6PzOofAO`. They were rejected during local preflight, before an upstream attempt. Those historical errors contain no capability details, so the exact offending field remains unconfirmed pending a client retry. Do not report the client compatibility incident as resolved solely because diagnostics were improved.

- Diagnostic commit `b2d9f9d2be3a`, runtime `v1.0.0-rc.35-fork.20260908.t163632.gb2d9f9d2be3a`. Candidate rejection now lists canonical missing capability names per validated protocol and distinguishes a policy with no verified endpoints. No request values, model names, custom endpoint paths or tool descriptions are added to diagnostics; routing eligibility is unchanged.
- Regression test failed with the original generic error and passed after the implementation. Full `go test ./service ./relay ./controller` passed; output `/private/tmp/protocol-capability-tests.log`. CI34205449920 passed all frontend/backend/Docker validation jobs. No database or RelayKit code/schema/dependency changes were made.
- Binary SHA256 `5165b5732fcd9b958f4469508c8f1b74f0666bd9f87cf4627b72fbb0cfbd9d2b` matched local/server copies. Formal binaries34205491474 and GHCR34205495519 published successfully. Server deployment again uses a local static binary on the existing runtime image, not an asserted GHCR digest.
- Backups and switch timestamps: `/opt/ops-backups/protocol-capability/`. Caddy moved to the verified canary at1788856780, the primary was recreated after the300-second drain, then Caddy returned to the healthy primary at1788857129. ModelRatio/CompletionRatio/CacheRatio/GroupRatio/RoutingPolicy hashes match the previous rollout; no protocol settings, prices, abilities or channel statuses were edited.
- Canary removed after namespace socket inspection at1788857282 confirmed zero active inbound API connections and zero external upstream connections; only four production containers remain. Caddy's original configuration was restored. Electron34205499743 also completed successfully. Client compatibility remains pending a reproduction with the new diagnostics.

### Native Responses and client namespaces (supersedes pending diagnosis above)

User confirmed `kimi-k3` and `hosted_tools`. Reproducing request construction with the same Codex CLI0.153.4 against a disposable localhost server found function, namespace and web_search declarations. The local stub returned a synthetic400 without calling any model; only request shape was retained at `/private/tmp/protocol-capability-build/codex-request-shape.json`.

- Code commit `d32685dfa7a3` treats namespace as a client tool container, inspects nested declarations without traversing schemas/arguments/descriptions, bounds recursion, and retains safe/strict cross-format namespace rejection. The added regression failed before the fix; `go test ./service ./relay ./controller` passed afterward (`/private/tmp/protocol-native-tests.log`). Independent scoped review found no blockers. No DB, RelayKit API or frontend logic changed.
- Direct bounded checks confirmed Kimi native Responses functions for k3, k3-256k, kimi-for-coding and kimi-for-coding-highspeed. K3 completed actual web_search SSE and a Codex-shaped namespace call with its namespace intact. Fire's `/api/plan/v3/responses` accepted kimi-k3 and produced ordinary function calls; its web/namespace probes did not produce corresponding calls, so those probes do not establish support. CTYun returned a valid non-stream incomplete/max-output response with usage; its web SSE probe timed out. SenseNova's current `/v1/responses` returned NOT_FOUND.
- Initial invalid-model endpoint checks unexpectedly received200 from Kimi. They must not be described as zero-inference or zero-cost probes; detailed usage was not retained for those initial small checks. Subsequent probes record returned usage. A timed-out probe without usage also does not prove no supplier consumption. Credentials stayed on the deployment server, in process memory, and were sent only to the corresponding configured provider or this gateway; no team request bodies were captured.
-14 existing channel policies updated through the existing settings application script. Snapshot `/opt/ops-backups/protocol-capability/native-settings/20260908T091423581265Z.json`; final input `native-policies.json` in its parent directory. Go DTO validation passed. ModelRatio/CompletionRatio/CacheRatio/GroupRatio/RoutingPolicy, abilities and unrelated channel settings/status/model/group hashes compare equal in `native-guard-before.json` and `native-guard-after.json`.
- Actual public gateway probes: request `202609080917320204704418268d9d6qfTcha6Q` completed a namespaced function call on kimi-k3; `202609080918374276341188268d9d6gwaYzljA` completed real web search on kimi-k3. Both returned200, response.completed and usage; each has one consume record, source/target openai_responses, Kimi supplier, VIP group, stream status ok. Quotas820 and20086 are existing internal ledger charges, not supplier invoices.
- Request `202609080924430026871138268d9d6B9UtDG5F` used kimi-for-coding with namespace and no hosted tool declaration. It also returned200/completed with the namespace intact, verifying the classification fix separately from enabling K3 hosted_tools.
- Runtime release `v1.0.0-rc.35-fork.20260908.t171033.gd32685dfa7a3`, static binary SHA256 `b2853bf9c07ca881d25ee4e9825e42da8d92c4b45d0c354b52bffebfe2c3b02f` matched local/server. Frontend assets rebuilt for this version. Deployment snapshots/timestamps live at `/opt/ops-backups/protocol-native-rollout/`; primary was drained for at least300 seconds before recreation. Detailed sanitized native/proxy probe results remain under `/opt/ops-backups/protocol-capability/`.
- Public traffic returned to the healthy primary at1788859520. Canary removed after namespace socket inspection at1788859761 showed no active API or upstream connections; four production containers remain. Public status confirms the release version. Binary34208845934 and Electron34208854425 succeeded; CI34208542342 passed frontend/backend jobs but its Docker validation and GHCR34208850270 were still running at handoff. Existing hourly automation now follows those final results once, alongside passive protocol/usage monitoring. Do not describe pending Docker publication as completed.

### Claude WebSearch follow-up and final hosted matrix

CI34208542342 and GHCR34208850270 subsequently completed successfully; all four code/release workflows are now green. Hourly automation no longer polls these completed jobs and now watches both Messages and Responses hosted-tool outcomes.

- User's next error was Claude WebSearch, not a failed Responses probe. Caddy logs identify two Claude Code `/v1/messages` requests at17:31:44 UTC+8. Their last candidate was CTYun, explaining the displayed hosted_tools/stream/tools list; that list does not describe every earlier candidate. Kimi's native Messages endpoint lacked the hosted_tools declaration even though the provider implements search.
- Direct K3 Messages probe returned200/end_turn with server_tool_use and web_search_tool_result. After applying the reviewed existing settings, public gateway request `202609080943049263579308268d9d6mwsNod67` returned200,19 search results, no tool errors, message_stop/end_turn and usage(input10073/output135), while retaining context_management. Evidence `/opt/ops-backups/protocol-capability/gateway-claude-web-probe.json`.
- Expanded matrix confirmed real search execution for Kimi k3, k3-256k, kimi-for-coding and kimi-for-coding-highspeed in both native Messages and Responses. The six additional non-K3 checks all ended normally; Messages returned14–20 results without tool errors. Fire kimi-k3 Messages returned10 results/end_turn. Fire kimi-k3-256k Messages returned10 results then max_tokens; its reported600 output tokens span two iterations despite per-step max_tokens512, so the requested cap must not be described as an absolute total consumption bound.
- Verified all12 active SenseNova kimi-k3 accounts, with12 distinct keys. All12 HTTP200 streaming probes yielded no valid search events or usage; this establishes no usable search stream in this test, not a claim about an empty HTTP body or permanent provider support. Hosted capability stays undeclared. Evidence `native-hosted-matrix.json` and `sensenova-accounts-hosted-matrix.json` under the same backup directory.
- Final `verified-hosted-policies.json` applies only verified search capabilities: all Kimi Messages/Responses defaults and model overrides; Fire kimi-k3 and kimi-k3-256k Messages model overrides. It does not enable CTYun hosted tools or grant any group/model access. Go DTO validation passed; latest14-channel snapshot `native-settings/20260908T095055234549Z.json` (earlier K3-only Messages snapshot `20260908T094005446175Z.json`). `hosted-guard-after.json` matches original price/ability/channel invariants exactly. Runtime remains d32685dfa7a3; these subsequent changes require no rebuild/restart.

### Search accounting completion

- Follow-up code `1e4b491ed36f` fixes missing Claude-format search counts when final ServerToolUse statistics are absent. It uses successful complete result blocks, deduplicates invocation IDs, excludes error representations and unfinished blocks, respects explicit reported0, and resets each upstream attempt. Observed counts suppress the implicit search-preview assumption. No prices, billing expressions, quota arithmetic, database models/drivers or RelayKit APIs changed; no historical charges were rewritten.
- Regression tests failed before the fix and passed afterward. Full `go test ./relay/channel/claude ./relay ./service ./controller` passed (`/private/tmp/protocol-search-billing-tests.log`). Existing test files cover result/usage precedence, error representations, retries and duplicate fee prevention. Independent review found an ErrorCode omission; it was fixed and verified before release, with no remaining blockers.
- Disposable complete-gateway verification passed13 requests/settlements, including both native formats, nonstream/SSE, reported/missing/zero statistics, errors, truncation and search-preview fee replacement. Equal successful search fixtures charged5120 in both formats; failures/zero statistics charged only120 token units, unfinished output only100. Total41540 exactly matched wallet and token deductions. Artifact `/private/tmp/protocol-search-billing-e2e/run-20260908-183808/results.json`; source fixture is in its parent verification script. The fixture used only local simulated providers and synthetic credentials.
- Live candidate request `202609081048245961493948268d9d6SdXNUTGz` returned200/end_turn,21 search results and no tool errors. Its sole consume log is native Claude→Claude, stream status ok, input10527/output40, quota20018, with exactly `web_search` count1 at the unchanged configured price10 per1000 calls. The21 hits were not billed as21 searches. Evidence `/opt/ops-backups/protocol-capability/gateway-claude-web-billing-probe.json`.
- Final runtime `v1.0.0-rc.35-fork.20260908.t183941.g1e4b491ed36f`; binary SHA256 `e89972a91fd7a74388307c9033460fb7d54325d53cb089288cc6b5d282368c81` matched local/server, and the Linux binary version was checked with VERSION unset. Static binary was deployed on the existing local runtime base; frontend assets rebuilt for the tag. Public status and primary agree; four production containers are healthy/running, canary removed after no active API/upstream connections. Rollback metadata `/opt/ops-backups/protocol-search-billing-rollout/`; primary drained at least300 seconds before recreation, Caddy restored at1788865109.
- CI34216637085, binaries34216909846, GHCR34216914032 and Electron34216918875 all succeeded. `billing-guard-before.json`/`billing-guard-after.json` match all configured pricing/ability/channel invariants. Monitoring gained readonly search_tool_charges aggregates, compiled successfully and returned the expected Kimi/Claude search count1, price10, incomplete0 on live PostgreSQL data. Its15-minute window initially contained no priced calls; the60-minute check included the candidate verification, so this proves monitoring integration rather than improved production workload statistics.


### 2026-09-08 共享协议模板与批量验证

与精确上游 v1.0.0-rc.36 合并。完整 Go 测试通过，RelayKit 独立 `GOWORK=off go build ./...` 通过；前端1058测试、类型检查、相关lint和生产构建通过。实际HTTP鉴权验证26个拒绝路径及18个安全Origin边界，并确认只读元数据无Key泄露、模板绑定和两项原生探测成功。真实浏览器另外完成目录保存、绑定预览/应用、3项报告创建/分批运行/结果应用，数据库确认报告持久化为applied。

数据库版本：SQLite3.50.4、MySQL8.0.46、PostgreSQL15.18。`go test ./controller -run TestProtocolProbeWorkflow -count=1` 使用 `PROTOCOL_TEST_DIALECT`/`PROTOCOL_TEST_DSN` 分别连接三种真实引擎，验证并发独占领取、版本冲突、原子应用、解绑、结果保留与中断恢复。新建、部署rc35升级、上游rc36升级共9场景通过；真实InitDB/InitLogDB再次启动，数据/索引/约束保留且二次启动DDL为0。原始报告 `/private/tmp/protocol-profiles-migration/run-20260908-215111/results.json`。

完整网关回归199检查通过，59条结算维持钱包和令牌守恒；搜索计费回归13场景通过。证据 `/private/tmp/protocol-e2e/run-20260908-215353` 和 `/private/tmp/protocol-profiles-rc36-billing-e2e.log`。现有39渠道整理为4个产品模板，3个模型差异；162个默认/映射/显式模型策略比较通过，除合并微秒差异的验证时间戳外，端点、顺序、能力、验证标志和损耗策略相同。原始验证时间戳留在部署快照。生产部署结果另记于下文。

最终独立审查的三项P2已修复并复核通过：默认代表探测忽略无关损坏旧渠道；界面允许显式清空继承验证时间；解除绑定先读取最新已保存策略，读取失败保留绑定，普通渠道保存同步失效相关缓存。补充41项前端聚焦回归、类型/lint/构建以及三数据库探测工作流通过。

生产迁移已完成：4模板分别绑定SenseNova24、火山2、Kimi12、天翼1，共39渠道。新主实例启动后恢复原Caddy文件，前后均确认退役实例入站3000和上游HTTP活动连接为0；临时实例已删除。备份 `/opt/ops-backups/protocol-profiles`，包括服务器本地600权限数据库备份和非秘密配置快照。价格/分组权限/渠道状态/其他设置/账号资源及额度范围的服务器端哈希完全相同。

首轮新批量报告共20项：SenseNova十二独立账号Chat流式，账号01/02通过，其余10项上游429；Kimi Messages联网搜索通过；天翼kimi-k3基础文本通过。Kimi/火山若干工具测试被强制tool_choice与思考模式冲突拒绝，已按真实证据修正共享测试样例为auto；仍要求实际函数名称、参数、namespace及正常终止。Kimi Responses联网的可选search_context_size被上游400明确拒绝，移除该可选调参后200正常结束；此样例实际仅返回reasoning/message，没有搜索调用及引用，因此继续记未知，不能据此删除以前已验证的联网能力。工具/搜索样例修复没有改真实业务请求，也没有改能力配置、价格或健康状态。全Go测试及聚焦transport/控制器回归、独立复核通过。

实际浏览器额外复现缓存A而服务器策略B的解除绑定操作，最终复制B（strict），确认不会还原旧缓存。实际完整网关通过共享模板，并验证改模板后不改渠道即可立即移除流式候选，再恢复模板。监测自动化保持每小时只读、变化才通知，不自动调用模型；当前最近一小时业务流量样本为0，不能声称成功率或成本已经改善。

### 2026-09-09 Responses 终止收尾回归

08:29–08:34 的 Windows Codex Desktop / kimi-k3 原生 Responses 请求集中记录 client_gone/context canceled，同期 SenseNova Chat 流正常。日志不足以证明每条历史请求的终止顺序；固定无用户正文的原生探测返回合法 response.completed，2.275秒完成、2.294秒EOF。

既有测试文件新增回归：原生与 Chat 转换的 completed/done/incomplete 终止后上游保持连接，旧代码全部等待EOF并在取消后报错；成功终止flush与客户端取消竞争也可稳定复现。修复显式结束上游扫描，仅原生已检查的终止交付可覆盖随后传输取消；转换路径保留取消检查。额外验证终止前取消和终止Write失败不报告成功、输入/输出/缓存用量保留。旧CustomEvent忽略Write错误的终止用例先失败，改为检查写入后通过。

完整 `go test ./...`、相关 `go vet`、前端生产构建通过。`go test -race -parallel 1 ./relay/channel/openai ./relay/helper` 通过；默认并行helper竞态检查发现 logger/logger.go:115 的既有全局计数器竞争，未修改的944ac1527基线也复现，不能宣称全项目无竞态。本次没有数据库、价格、权限或模板变更；生产版本与切换证据记录在服务器 `/opt/ops-backups/responses-stream-finish/`。

### 2026-09-09 Messages and auto-review follow-up

- Actual Codex0.153.4 isolated localhost reproduction produced `codex-auto-review`, leading developer `additional_tools` (namespace with grammar custom tool plus function), and `text.format=json_schema`. No real credentials, team history, or upstream calls were used. It explains a reproducible `unsupported_content` path, without claiming the historical production body was captured.
- Eight bounded provider checks: Sense47/Fire6 DeepSeek strict JSON Schema samples completed and matched schema; Sense24 basic Messages and Sense34 tool-history Messages returned HTTP200 with zero bytes, while the opposite account/request pairs completed with `message_stop`. Both Chat contrasts returned429 ModelAccountTpmRateLimitExceeded. Three additional Fire native Responses checks verified schema output, but not namespace/custom tool execution (completed plain text despite required tool choice, below token limit).
- Regressions reproduced before fixes: Claude/native, Chat, Gemini, and Responses waited for upstream EOF after message_stop; native terminal/cancel races and failed output writes were misclassified. Added deterministic tests in the existing Claude test file; retained empty/missing-terminal and partial-usage cases. AWS retains its existing client-cancel contract.
- Protocol regression tests cover opaque application JSON tool results and canonical unknown-content diagnostics with private values excluded. Unknown protocol input still fails closed.
- Validation: full root `go test ./...` passed; Claude/helper/AWS/OpenAI package tests and focused Claude/AWS race tests passed. No schema, database driver, or authentication changes. Frontend source is unchanged. Correction: the initial manual artifact mistakenly embedded an empty placeholder index.html, so the earlier frontend-bundle validation claim was incorrect; see the recovery record below.
- No Kimi variant substitution was made. Preserve same-model constraints, pricing and provider order. Auto-review support must not be claimed complete until the actual schema and tool semantics are validated on its selected provider.

### 2026-09-09 blank-page recovery

The manually compiled264eff552 artifact had an empty embedded `web/dist/index.html` (homepageHTTP200, zero bytes). The API health endpoint and relay tests did not detect it. Replaced it with the same tag's GitHub Actions Linux binary after verifying published SHA256 `df25838e4bffd163280764deada28219e31718de7d27fe670145b9eca4826096`; no source behavior, pricing, permissions or model mappings changed. Local image tag ends in `-release` to distinguish it from the broken manually assembled artifact; the application still reports its canonical release version.

Candidate homepage returned1166bytes and all7 entry JS/CSS assets returned nonempty data with correct MIME types. A real browser rendered both `/` and `/dashboard/overview` (overview heading visible). New read-only `ops/verify-web-assets.py` correctly rejected the still-running bad container and passed the release candidate; it uses small ranged reads for assets. Evidence is retained in `/opt/ops-backups/web-recovery/`. API status alone is not a frontend acceptance check.

### 2026-09-09 Codex auto-review Responses Lite bridge

- Commit `858566d76330` implements a configuration-driven `responses_lite_bridge`; it contains no supplier, model or alias checks. The existing UI mapping keeps `codex-auto-review` on `deepseek-v4-flash`. Only the verified DeepSeek Chat endpoint overrides in the SenseNova and Volcengine shared profiles gained the feature. Model permissions, supplier order, prices and cache prices were unchanged.
- The bridge flattens `additional_tools` namespaces into request-scoped Chat function aliases, rewrites custom/function history, converts Responses developer messages to Chat system messages, and maps non-stream and SSE tool calls back to the original kind, namespace and name. Malformed custom arguments fail closed. Native Responses endpoints cannot advertise this bridge.
- Full root tests and vet passed. RelayKit passed independent `GOWORK=off go build ./...`, full tests, vet and focused race checks. The frontend passed1064 tests, typecheck, affected lint/format checks and production build. Full frontend lint still reports unrelated pre-existing findings outside the changed file.
- Direct provider checks exercised all12 separate SenseNova keys and Fire6. SenseNova account54 and Fire6 each completed the combined streaming/tool/schema Chat shape after changing the unsupported developer role to system; other SenseNova keys produced independent rate, quota or request errors. This supports account-scoped limits without claiming IP independence from a single host.
- Public gateway request `202609090418187328902438268d9d6o7oBNkoi` returned200 with `response.custom_tool_call_input.delta/done`, `response.completed`, original `functions.exec`, exact raw input `text('ok')`, and no internal alias leakage. Request `202609090423055431380088268d9d6g5I7zMuM` returned200/completed and exact strict JSON `{probe_outcome: approved, probe_code: 7}`. Both routed Responses→Chat through Fire6 with the mapped upstream model `deepseek-v4-flash`; no Kimi variant substitution occurred.
- Runtime `v1.0.0-rc.36-fork.20260909.t114704.g858566d76330` serves the healthy primary. Homepage assets and the authenticated dashboard rendered in a real browser. CI34308462212, binaries34308523202, GHCR34308525652 and Electron34308528101 all succeeded. Sanitized production evidence and guarded profile snapshots are under `/opt/ops-backups/codex-auto-review/`.
- The passive monitor now emits `auto_review_correlation` and `auto_review_routes`; it reads only aggregate log metadata. A completed request after upstream errors is reported separately from an exhausted request, and incomplete streams remain an alert condition.
- CTYun stayed disabled throughout build, rollout and live bridge verification. It was re-enabled only after those checks and monitor deployment; all12 restored abilities belong exclusively to VIP, so it remains the final VIP fallback and is unavailable to default/plus/dev. Final snapshots are under `/opt/ops-backups/codex-auto-review/20260909T042851Z/`.

### 2026-09-09 authentication and account-capacity rollout

- Commit `19b36c7d9` separates anonymous login (`60/1200s/IP`) and routine Refresh (`120/1200s/IP`) from the old shared critical-operation window. All login completion stages share the login counter. A transient/429 Refresh result remains a transient route error and no longer clears the local session or redirects a protected page to sign-in. The defaults are environment configurable.
- SenseNova now uses Redis-backed, account-wide 60-second load buckets with soft limits of 20 attempts and 300,000 estimated input tokens. Selection prefers the least-loaded eligible account, lets affinity migrate away from projected saturation, and retains the configured supplier order. Actual429 creates an account-wide cooldown with bounded exponential backoff from60 to600 seconds; Redis preserves cooldown and load across process replacement. The initial1,000,000-token setting was reduced after a later five-minute burst of10 requests produced19 rejected attempts; successful prompts in that burst were about280,000 tokens each, so the tighter setting limits these large requests to one per account window.
- The monitor reports `client_gone` only as `downstream_cancellations`. Upstream EOF/scanner/terminal failures remain `upstream_incomplete_streams`. It also reports per-request provider rate-limit amplification, per-account load, and authentication endpoint HTTP status; only upstream failures, exhausted provider limits, or auth429/5xx raise alerts.
- Verification passed: full root `go test ./...`, `go vet ./...`, `go build ./...`; RelayKit independent `GOWORK=off go build ./...`; focused race test; frontend117 files/1073 tests, typecheck and production build. Routing regressions passed on SQLite3.50.4, PostgreSQL15.19, and MySQL8.0.46. No schema, migration, ORM, pricing, model permission, or protocol-capability change was made.
- Runtime `v1.0.0-rc.36-fork.20260909.t182711.g19b36c7d9150`; local/server binary SHA256 `351d068e43a0ced15b2f13422ca444bf921eee1c9143fb40a37ab773ef22f66c` matched. Internal and public status agree, the container is healthy, homepage is1166 bytes, and all7 entry JS/CSS assets are nonempty with expected MIME types. Rollback material and the prior Compose override are under `/opt/ops-backups/auth-routing-reliability/20260909T182711`; database backup `new-api_20260909_183559.sql.gz` completed before replacement.
- The first passive post-start sample contained19 Plus DeepSeek requests. Nine met SenseNova rate limits, producing18 rejected attempts; all19 requests completed, none exhausted all candidates, and the maximum was4 rate-limit attempts for one request. That is0.95 rejected attempts per business request versus the earlier comparable60-request sample's211/60=3.52, a preliminary73% reduction. The sample is short and does not establish a long-term rate. It had zero upstream incomplete streams, zero downstream cancellations, zero missing stream metadata, and zero CTYun consumption.
- CTYun is status2 and all12 VIP-only abilities are disabled. The stored routing order still keeps it as VIP's final tier, ready for a later explicit enable decision after observation; it is unavailable to Default, Plus, and Dev while disabled.
