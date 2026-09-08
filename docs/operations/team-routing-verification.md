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
