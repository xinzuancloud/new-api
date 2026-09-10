# 亲和性运行时失败处理修复（skip_retry 三缺陷）

状态：待实施（2026-09-10 审计提出，用户确认直接修复）

## 问题（三缺陷叠加，skip_retry_on_failure=true 时放大为事故）

1. **运行时失败不清条目**：亲和条目唯一清除点在选中阶段（middleware/distributor.go:155-157）。
   粘性渠道运行时失败（429/500/超时/断流）无任何路径清除 → 条目持续指向坏渠道至 TTL（3600s）。
2. **skip_retry 作用域过大**：`ShouldSkipRetryAfterChannelAffinityFailure` 在显式 flag 未设置时回退读规则 meta
   （service/channel_affinity.go:626-642）——只要请求匹配出 key（含缓存未命中的首次请求、亲和渠道根本没被使用），
   该请求任何失败都跳过全部重试。
3. **Redis 读失败误注入**：`setChannelAffinityContext` 在 cache.Get 之前注入 meta（:596-617），
   GET 失败仅 SysError 后 return——亲和没参与选渠，skip_retry 却已在 context 里。

## 修复

- `processChannelError` / 重试决策处：本次尝试使用了亲和渠道且失败（429/5xx/超时/断流）→ 调 `ClearCurrentChannelAffinityCache`；
- `ShouldSkipRetryAfterChannelAffinityFailure` 只看显式 flag（本次确实走了亲和渠道才置位），删除 meta 回退；
- `setChannelAffinityContext`：cache.Get 失败时不注入 meta（或显式 `c.Set(ginKeyChannelAffinitySkipRetry, false)`）。
- 修复后 `skip_retry_on_failure=true` 语义变为"本次亲和渠道失败后快速失败且清条目"，开着也安全。

## 验证

- 单测：粘性渠道 429 → 条目被清 → 客户端同 key 重试落到健康渠道；Redis GET 故障注入 → 重试行为与无亲和一致。
- 生产回归：观察亲和命中切换分布（monitor 的 routes 聚合），无渠道钉死超 TTL。
