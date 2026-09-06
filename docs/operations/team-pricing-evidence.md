# Team model pricing evidence

Reviewed 2026-09-06 (Asia/Taipei). Research only: no production settings or source code changed. Rates below are provider publications, not proof of this deployment's purchase terms. `Unknown` means no authoritative rate was verified; it must not be interpreted as zero.

## Verified public API references

All token prices are per **1,000,000 tokens**, in the explicitly stated currency. Input means uncached input. No currency conversion has been applied.

| Model / direct provider | Currency | Input | Output | Cache read | Cache write | Context / conditions |
| --- | --- | ---: | ---: | ---: | --- | --- |
| `kimi-k3` / Kimi international | USD | 3 | 15 | 0.30 | No separate write rate published in cited table | 1M; no length surcharge listed |
| `kimi-k3` / Kimi China | CNY | 20 | 100 | 2 | No separate write rate published in cited table | 1M |
| `kimi-k2.6` / Kimi international | USD | 0.95 | 4 | 0.16 | No separate write rate published in cited table | 262,144 tokens |
| `kimi-k2.6` / Kimi China | CNY | 6.5 | 27 | 1.1 | No separate write rate published in cited table | 256K |

Sources: [Kimi international platform](https://platform.kimi.ai/), [Kimi China platform](https://platform.kimi.com/), [K2.6 pricing](https://platform.kimi.ai/docs/pricing/chat-k26), [K3 capabilities/pricing page](https://platform.kimi.ai/docs/pricing/chat-k3). International prices exclude applicable taxes. The K3 detailed page's fetched text omitted its price table, so prices were cross-checked against the official platform homepage.

| Model / DeepSeek direct API | Currency | Peak input / output / read | Off-peak input / output / read |
| --- | --- | --- | --- |
| `deepseek-v4-flash` (Flash-0731) | USD | 0.44 / 1.32 / 0.014 | 0.22 / 0.66 / 0.007 |
| `deepseek-v4-pro` (Pro-0813) | USD | 1.32 / 3.96 / 0.044 | 0.66 / 1.98 / 0.022 |
| `deepseek-v4-flash` | CNY | 3 / 9 / 0.10 | 1.5 / 4.5 / 0.05 |
| `deepseek-v4-pro` | CNY | 9 / 27 / 0.30 | 4.5 / 13.5 / 0.15 |

Both list 1M context and 384K maximum output, without context-length price tiers. Peak hours are Monday–Friday 01:00–04:00 and 06:00–10:00 UTC (09:00–12:00 and 14:00–18:00 UTC+8); all other hours cost half. No separate cache-write rate is listed. Sources: [live USD pricing](https://api-docs.deepseek.com/quick_start/pricing/), [live CNY pricing](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/). **Search-index excerpts returned obsolete, substantially lower rates; the live tables above supersede those excerpts.** These are not verified CTYun or Agent Plan tariffs.

| Model / Z.ai direct API | USD input | USD output | USD cache read | Cache storage/write | Conditions |
| --- | ---: | ---: | ---: | --- | --- |
| `glm-5.3` | 1.4 | 4.4 | 0.26 | Storage temporarily free; no separate write-token rate listed | 1M context, 128K max output; no price threshold listed |
| `glm-5.2` | 1.4 | 4.4 | 0.26 | Same | 1M context, 128K max output; no price threshold listed |
| `glm-5.3-flash` | 0.075 | 0.25 | 0.015 | Same | Promotional prices through September 9, 2026, 24:00 UTC+8 |
| `glm-5.3-flash` list price | 0.15 | 0.50 | 0.03 | Same | Use for a durable default unless promotion expiry is explicitly implemented |

Sources: [live Z.ai pricing](https://docs.z.ai/guides/overview/pricing), [GLM-5.3 specifications](https://docs.z.ai/guides/llm/glm-5.3), [GLM-5.2 specifications](https://docs.bigmodel.cn/cn/guide/models/text/glm-5.2). GLM-5.3-Flash context and China-specific cash prices were not verified. Public tables do not establish independent tariffs for the deployment's `glm-5.3-256k` or `glm-5.2-256k` aliases.

| Model / Volcengine public API | CNY input | CNY output | CNY cache read | Other cache charge |
| --- | ---: | ---: | ---: | --- |
| `doubao-seed-2.1-turbo` | 3 | 15 | 0.6 | 0.017 CNY / million cached tokens / hour |
| `doubao-seed-evolving` | 6 | 30 | 1.2 | Same |
| `doubao-seed-2.0-lite` starting rate | 0.6 | 3.6 | 0.12 | Same |

The official [Doubao product page](https://www.volcengine.com/product/doubao) lists the current 2.1/Evolving prices. Its [older indexed page](https://www.volcengine.com/product/doubao/) lists 2.0 Lite **starting** prices. [Coze billing documentation](https://www.volcengine.com/docs/84458/1585097?lang=zh&redirect=1) publishes Lite cash input/output tiers in thousands of tokens: ≤32K: 0.6/3.6 CNY per million; (32K,128K]: 0.9/5.4; (128K,256K]: 1.8/10.8. That is supporting evidence of length sensitivity, **not verification of the deployment's Ark/Agent Plan tariff**. Full Ark cache-tier rates and 2.1/Evolving context thresholds remain unverified. The hourly storage charge cannot be encoded as a one-time `cc` price without storage duration.

| Model / MiniMax international public API | USD input | USD output | USD cache read | Context |
| --- | ---: | ---: | ---: | --- |
| `MiniMax-M3` | 0.30 | 1.20 | 0.06 | ≤512K |
| `MiniMax-M3` | 0.60 | 2.40 | 0.12 | 512K–1M |

The official [API enterprise pricing tab](https://platform.minimax.io/subscribe/token-plan?tab=api-enterprise) exposes these prices in its indexed text and labels them a permanent 50% reduction from 0.6/2.4/0.12 and 1.2/4.8/0.24. The live page is client-rendered and did not expose its table to the fetcher. M3 cache-write pricing and the exact numeric interpretation of the 512K boundary remain unverified; do not borrow M2.7's $0.375 cache-write price from the [older pay-as-you-go document](https://platform.minimax.io/docs/guides/pricing-paygo). Preserve this deployment's lowercase alias `minimax-m3` if already mapped; do not infer alias equivalence solely from spelling.

## Production purchase modes and unresolved aliases

| Deployment model / route | Finding and configuration consequence |
| --- | --- |
| `kimi-k3` / `k3` via Kimi Code | Official subscription ID is `k3`; public API ID is `kimi-k3`. Keep channel mapping explicit. Public USD/CNY price is reference value, not subscription cash cost. |
| `kimi-k3-256k` | Official Kimi Code ID is `k3-256k`; fixed 256K context. No independent public cash tariff verified. |
| `kimi-for-coding` | Currently maps to Kimi K2.7 Code with 256K context; stable ID can change underlying model over time. No per-token subscription cash price verified. |
| `kimi-for-coding-highspeed` | Same coding capability, about 5–6× output speed and about 3× subscription quota consumption. No per-category cash rate verified. |
| `glm-5.3-256k`, `glm-5.2-256k` | Provider/plan-specific aliases; separate cash and cache rates unknown. Do not silently equate them with public 1M tariffs. |
| `sensenova-6.8-flash-lite` / free route | Official preview documentation confirms the ID and a free token plan. The operator reports this route as free. Set its intended team charge explicitly to zero if that remains the policy; never use an absent price as a proxy for free. Paid overage, numeric context limit, and cache prices not verified. |
| All models through Volcengine Agent Plan | The official API exposes AFP allowance/usage, independently of token counts. Public API CNY prices cannot establish actual subscription AFP consumption or the marginal cash cost. Per-model AFP coefficients and overage settings need the account's current plan evidence. |
| All models through CTYun metered route | No authoritative current per-model input/output/cache table for these deployed aliases was located. Exact CTYun product, model SKU, region and account tariff are required. Do not substitute the model author's public price and call it CTYun cost. |

Sources: [Kimi Code model mappings](https://www.kimi.com/code/docs/en/kimi-code/models.html), [Kimi Code benefits/quota comparison](https://www.kimi.com/en/help/kimi-code/membership-guide), [SenseNova official API repository](https://github.com/OpenSenseNova/SenseNova6.8/blob/main/API.md), [Volcengine GetSeatAFPUsage API](https://api.volcengine.com/api-docs/view?action=GetSeatAFPUsage&serviceCode=ark&version=2024-01-01), [CTYun Xirang product](https://www.ctyun.cn/products/ctxirang). Community articles hosted under `/article/` on Volcengine were excluded as pricing authority.

## Local cache-pricing diagnosis

At review time, `setting/ratio_setting/cache_ratio.go:159` returns `(1,false)` for missing read ratios and `(1.25,false)` for missing write ratios. `relay/helper/price.go:120` discards the presence flags, so legacy billing uses those fallback rates. However, `model/pricing.go:391` only publishes read/write ratios when explicitly present. `web/src/features/pricing/lib/price.ts:76` returns `NaN` for absent ratios, and model detail/card components hide them. Therefore a blank cache price does **not** mean no cache charge. This is an API/display mismatch independent of actual provider pricing.

With expressions, `pkg/billingexpr/expr.md` establishes a different explicit contract: omitted cache variables remain in base input pricing; referencing `cr`, `cc`, or `cc1h` separates their tokens. `len` controls context tiers and must not be replaced with cache-reduced `p`.

## Actionable configuration

### Approved team ledger convention (follow-up)

The operator subsequently confirmed that existing input/output prices must stay unchanged, while verified model-author cache discounts should be applied as the **team ledger policy**, independently of upstream invoices. The following ratios divide the author's cache-read price by its uncached-input price; multiplying an existing input price by these ratios preserves that policy. USD and CNY tariffs can produce slightly different ratios because their published prices are independently rounded.

| Model | USD-reference cache/input ratio | CNY-reference cache/input ratio | Applicability |
| --- | ---: | ---: | --- |
| `kimi-k3` / `k3` | 0.1 | 0.1 | Verified K3 pricing and Code mapping |
| `kimi-k3-256k` / `k3-256k` | 0.1 | 0.1 | K3 model mapping verified; applying the K3 public discount to the 256K subscription variant is a ledger policy inference |
| `kimi-for-coding` | 0.2 | 0.2 | Official mapping to K2.7 Code; its public rates are USD .19/.95, CNY 1.3/6.5 |
| `kimi-for-coding-highspeed` | 0.2 (inferred) | 0.2 (inferred) | Same underlying model verified; a separately published highspeed cache/input ratio was not found |
| `kimi-k2.6` | 0.16842105263157895 | 0.16923076923076924 | USD .16/.95; CNY 1.1/6.5 |
| `deepseek-v4-flash` | 0.031818181818181815 | 0.03333333333333333 | Same ratio in peak/off-peak periods; USD .014/.44, CNY .1/3 |
| `deepseek-v4-pro` | 0.03333333333333333 | 0.03333333333333333 | Same ratio in peak/off-peak periods |
| `glm-5.3`, `glm-5.2` | 0.18571428571428572 | Unknown | USD .26/1.4 |
| `glm-5.3-flash` | 0.2 | Unknown | Same ratio at promotional and list prices |
| `glm-5.3-256k`, `glm-5.2-256k` | 0.18571428571428572 (inferred) | Unknown | Only if routing confirms these aliases are context-limited forms of the matching base model; no independent alias tariff verified |
| `doubao-seed-2.0-lite`, `doubao-seed-2.1-turbo`, `doubao-seed-evolving` | Unknown | 0.2 | Lite ratio verified at starting tier only; do not infer unverified higher-tier rates |
| `minimax-m3` | 0.2 | Unknown | Official indexed USD table, both context tiers; live-rendered verification limitation above applies |
| `sensenova-6.8-flash-lite` | Not meaningful for zero-price route | Not meaningful | Explicit free team policy; no paid published discount verified |

K2.7 prices are published on the same [official international](https://platform.kimi.ai/) and [China](https://platform.kimi.com/) platform pages cited above; [Kimi Code mapping documentation](https://www.kimi.com/code/docs/en/kimi-code/models.html) identifies the subscription model. All other ratios are arithmetic derived from the cited tables, not separately published percentages. No cache-write ratio is recommended where no authoritative write tariff exists.

### Implementation recommendations

1. Preserve all explicit administrator prices and alias mappings. Do not bulk replace ratio maps or migrate existing legacy prices just because a public reference price is available.
2. Separate **team charge policy**, **public API reference value**, and **actual upstream cost/plan consumption**. A subscription may have zero incremental cash for a request while consuming valuable allowance; token-priced team budgets can be an explicit internal allocation policy, but should not be labeled measured provider cost.
3. Expose effective legacy cache fallback prices with an indication that they are defaults, or require explicit cache settings for the affected models. Do not present absent read/write fields as free. Keep `0` distinct from absent.
4. For newly introduced public-reference defaults, use self-contained USD expressions in `setting/billing_setting/builtin_billing.go`, retaining existing override precedence. Verified uncomplicated examples: Kimi K3 `tier("base", p * 3 + c * 15 + cr * 0.3)`; K2.6 `tier("base", p * 0.95 + c * 4 + cr * 0.16)`; GLM-5.3/5.2 `tier("base", p * 1.4 + c * 4.4 + cr * 0.26)`. These are reference prices, not proposed automatic changes to subscription or CTYun routes.
5. DeepSeek needs the verified weekday/hour schedule if matching direct API pricing. Do not freeze today's Sunday off-peak rate as an all-week price. Test boundaries and ensure settlement uses the request's intended time basis.
6. Do not invent a cache-write premium. Missing published write pricing, temporary storage promotions, hourly storage pricing and explicit free cache writes are different facts. Reconcile actual upstream usage fields before choosing `cc`/`cc1h` treatment.
7. Resolve CTYun tariffs, Agent Plan AFP coefficients, MiniMax M3's exact boundary/write treatment and alias-specific rates before claiming provider-cost accounting is complete. A safe interim rollout can fix cache visibility, preserve existing charges, and label unverified cost estimates clearly.
