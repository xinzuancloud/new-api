# Team routing verification

Current runtime: `v1.0.0-rc.33-fork.20260907.t100757.g3f015753`; latest change is documented in the 2026-09-07 section below. The initial 2026-09-06 evidence is retained for comparison.

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
