# Team routing and reliability

Approved by the user in this task on 2026-09-06. Production changes and a custom build are authorized; this supersedes the earlier server-only official-build restriction.

## Binding behavior

Default keeps only deepseek-v4-flash and sensenova-6.8-flash-lite through SenseNova. Plus retains its existing approved models: SenseNova light/flagship; Volcengine Flash/256K channel list; Kimi kimi-for-coding and kimi-k3-256k. Dev retains its existing model permissions. Plus/dev route SenseNova -> Volcengine -> Kimi. VIP routes Kimi -> Volcengine -> SenseNova -> CTYun. No cross-group fallback may grant CTYun to non-VIP. Existing provider lists remain the model authorization source. k3 is an alias of kimi-k3, not another model.

## Implementation

Keep native group/model/credential management and existing channel tags. Add optional, atomic `RoutingPolicy` JSON setting with UI for per-group ordered channel tags, attempts per tag, temporary 429 cooldown, quota-keyword cooldown, and whole-request deadline. Defaults disabled for other installations. No group names/provider names/channel IDs in production code. Policy selects only within already authorized group/model and existing channel constraints, never retries an already attempted channel, bounds same-provider attempts, and reserves room for later providers within the existing retry budget. Affinity may choose among healthy channels of the highest available policy tier, never override provider order. Temporary cooldown is per channel and requested model; durable credential failures retain native auto-disable. Native request and streaming accounting stay intact; bind upstream requests to client cancellation and stop retries on cancellation.

No new schema, ORM, or driver dependencies. Exercise routing via real SQLite/MySQL/PostgreSQL and cache on/off. Keep relaykit independently buildable. Use existing UI/form/i18n conventions and all seven locales.

## Pricing

Audit existing price options and exact models against primary published sources. Fill verified missing cache prices using existing settings; preserve explicit admin overrides. Never manufacture newer-model or subscription prices. Label unverified values and separate internal billing from actual provider cost. New built-in prices, if necessary, must use billing expressions.

## Release and observation

Record sanitized baseline. Back up production DB and deployment config on server before mutations. Build identifiable fork release; validate isolated instance against disposable DB/Redis and fake upstream before cutover. Preserve rollback image/config. Document exact whitelist, configuration knobs, price evidence, verification and rollback. Run passive aggregate monitoring after deployment (no model probes), notify meaningful changes only. Compare final HTTP outcomes, retries per request, latency, cooldown/route events, cache usage and metered consumption; never interpret attempt-error ratio as final failure rate.
