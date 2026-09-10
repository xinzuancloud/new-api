# 亲和性 Redis 模式三小件

状态：待实施（2026-09-10 审计第 10 条，用户确认排期）

## 问题

1. `max_entries=100000` 在 Redis 模式完全不生效（pkg/cachex/hybrid_cache.go 仅约束内存 LRU）——
   条目数仅受 TTL×新增会话速率约束，高 churn 场景无兜底。
2. 每个匹配请求 4 次 Redis 操作（亲和 GET+SETEX 滑动续期 + 用量统计 GET+SETEX），
   Redis 抖动时读路径最多增加 2s 首字节延迟（hybrid_cache.go:15 超时用 context.Background）。
3. `getChannelAffinityCache()` 用 sync.Once 在首次调用时冻结 MaxEntries/DefaultTTLSeconds（channel_affinity.go:83-111），
   之后改容量/TTL 需重启才生效。

当前量级（Redis 1.81MB/35 keys）不构成问题，本修复面向规模增长。

## 方向

- 写路径去重：同 key 30-60s 内不重复 SETEX 续期（进程内 lastWrite 表即可）；
- 用量统计进程内聚合、批量刷 Redis；
- sync.Once 改为可重建：配置变更时检测 MaxEntries/DefaultTTLSeconds 差异并重建缓存句柄；
- 文档化"Redis 模式 max_entries 不生效"，或对亲和 namespace 做定期 trimming（SCAN+计数，超阈值时按 TTL 倒序裁）。

## 验证

- 单测：同 key 连续 10 请求，Redis SETEX 次数 ≤ 2（去重窗口内）；配置变更后新容量无需重启生效。
- 压测：Redis 故障注入（iptables DROP），首字节延迟劣化 ≤ 去重窗口 + 单次超时。
