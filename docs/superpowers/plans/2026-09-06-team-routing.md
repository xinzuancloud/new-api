# Team routing implementation plan

**Goal:** Enforce approved team group permissions/provider order while improving reliability, cache pricing and observable metered fallback.
**Architecture:** Native authorization + optional tag-ordered policy; transport cancellation; existing price settings; reproducible deployment and passive audit.
**Tech Stack:** Go/Gin/GORM, React/TypeScript/Bun, PostgreSQL/Redis/Docker.
**Spec:** ../specs/2026-09-06-team-routing-design.md

## Global constraints

- No hardcoded groups/providers/model names in production code. No non-VIP access to CTYun.
- Preserve protected new-api/QuantumNous information and explicit price overrides.
- No schema/driver upgrades. Verify real SQLite, MySQL and PostgreSQL for database-related behavior; verify cache on/off.
- Follow AGENTS.md, web/AGENTS.md, frontend i18n skills; no new dependencies needed.
- Production deployment is authorized, after review and validation. Never force-push or overwrite tags.

## Work items

- [x] 1. Capture sanitized baseline/allowlists and authoritative pricing evidence. Price research independent of implementation.
- [x] 2. Add deterministic tests in service/channel_select_auto_groups_test.go for reversed group tag order, exhausted/cooling channels, no repeats, bounded attempts/fallback, strict affinity and cache-off parity. Add validated immutable RoutingPolicy configuration and tag/exclusion filters using existing candidate matching.
- [x] 3. Add regression for cancelled upstream HTTP request in existing relay transport tests; bind context, preserve body metadata; stop cancelled/deadline retries in controller.
- [x] 4. Expose policy through model settings UI and complete translations. Test validation/save round-trip, typecheck/lint/build.
- [x] 5. Review changes; run targeted Go tests and real three-dialect integration; relaykit independent build; full production build.
- [x] 6. Retain original image and metadata/settings rollback snapshot; verify a local isolated application against fake upstream without paid traffic. Apply native tags/model aliases/approved policy/cache options atomically; cut over with rollback available.
- [x] 7. Record post-cutover evidence, maintain operations runbook and create passive follow-up monitoring automation.

## Decisions and progress

- User approved stated design; do not re-request routine implementation or deployment approval.
- Preserve current Plus/Dev exact model lists instead of inferring additional access.
- Worktree: .worktrees/team-routing, branch codex/team-routing, base efae3eff.

- Deployment verification used local synthetic data after automatic review rejected extra production DB/credential copies and test-role creation. Both rejected actions did not run.
- Runtime uses the default `docker-compose.override.yml` so ordinary Compose restarts retain the custom image.
- Production verified and hourly monitor created; CTYun enabled last at 14:44:38 UTC+8.
