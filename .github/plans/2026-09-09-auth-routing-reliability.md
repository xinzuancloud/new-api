# Authentication and routing reliability implementation plan

Goal: stop page refreshes from exhausting login protection, distinguish downstream cancellations from upstream stream failures, and spread SenseNova traffic across account resources before falling back to paid suppliers.

Architecture: keep authentication abuse controls server-side with separate fixed-window IP scopes for login and refresh. Pass the root authentication result through the TanStack Router context so nested guards do not repeat the same refresh or turn transient failures into sign-out redirects. Store routing cooldown and rolling account load in Redis with an in-memory fallback, and configure capacity by channel tag in the existing Routing Policy option/UI. Preserve group/model permissions, exact model names, and configured supplier order.

Security controls: login stages share one 60 requests / 20 minutes / IP scope; refresh uses 120 requests / 20 minutes / IP. Both limits and durations are environment configurable. Existing account/session validation, origin checks, generic authentication errors, audit logging, session rotation, and user-scoped security-operation limits remain authoritative. This follows OWASP ASVS 5.0 and the Authentication and Session Management Cheat Sheets for server-side throttling, session protection, and auditable failures without treating shared-IP throttling as account lockout.

## Tasks

- [ ] Add focused middleware regressions for independent login, refresh, and critical scopes, then add environment-backed defaults and route wiring. Document the environment variables in existing configuration references.
- [ ] Add frontend regressions proving one root refresh result is reused by nested guards and a transient refresh failure does not redirect an authenticated route to sign-in. Implement the typed root route context and guard outcome handling.
- [ ] Extend existing routing-policy tests for tag capacity validation, least-loaded account selection, capacity-based affinity migration, Redis-backed cooldown persistence, dynamic 429 backoff, and paid fallback only after every locally available higher-tier account is unavailable.
- [ ] Implement Redis rolling load buckets and cooldowns keyed by account resource plus mapped model. Reserve estimated prompt load before each upstream attempt, prefer the lowest projected load within the current tag, skip locally saturated accounts, and use bounded exponential cooldown after real 429 responses. Keep memory behavior as a fail-open fallback when Redis is disabled or unavailable.
- [ ] Add tag-capacity rows and maximum 429 cooldown to the existing Routing Policy settings UI using shared form components. Add all user-facing strings to every supported locale through the project i18n script.
- [ ] Change the existing routing monitor to report `downstream_cancellations` for `client_gone` and `upstream_incomplete_streams` for other stream errors. Alert only on the upstream category and update the existing operations references.
- [ ] Run focused tests first, then root Go tests/build, frontend tests/typecheck/lint/build, and the real SQLite/MySQL/PostgreSQL matrix if database behavior is touched. Review the final diff and security controls before deployment.
- [ ] Build and deploy an immutable image, apply the routing capacity configuration without enabling CTYun, verify login/refresh behavior and routing state on the live stack, then update the existing monitor automation and observe production logs for regressions.

## Production configuration

- Login: 60 requests per 1200 seconds per client IP.
- Refresh: 120 requests per 1200 seconds per client IP.
- SenseNova: use a 60-second rolling window with 20 requests and 1,000,000 estimated input tokens per account resource. An idle account always gets one attempt even when a single request exceeds the token window; subsequent requests move to lower-load SenseNova accounts.
- Supplier order remains `sensenova → volcengine → kimi` for plus/dev, `kimi → volcengine → sensenova → ctyun` for vip, and SenseNova-only for default. CTYun stays disabled until the user’s remaining acceptance checks are complete.
