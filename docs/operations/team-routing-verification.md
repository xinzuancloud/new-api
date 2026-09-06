# Team routing verification — 2026-09-06

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
| PostgreSQL | 15.19 | `NEW_API_ROUTING_TEST_DSN='<local disposable PostgreSQL DSN>' go test ./service -run TestRoutingPolicy -count=1` | Passed |
| MySQL | 8.0.46 | `NEW_API_ROUTING_TEST_DSN='<local disposable MySQL DSN>' go test ./service -run TestRoutingPolicy -count=1` | Passed |

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
