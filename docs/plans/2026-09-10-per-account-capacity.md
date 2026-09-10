# 容量窗口：竞态收窄 + 实测调参（原"per-account 容量窗口"）

状态：部分已实施（2026-09-10）；**实施前代码复核更正了审计阶段的错误定性**。

## 更正（重要）

审计时判断"容量窗口是 per-tag 共享（12 账号共用一个 20 req/60s 窗口）"。**代码复核结论：记账与准入
本来就是 per-account 的**——`routingAccountKey`（service/routing_policy.go:301）以 account_resource
（无别名时 channel:id）为键，`RecordRoutingAttempt` 与 `SelectPolicyChannel` 的饱和检查都按账号独立桶
对比 tag 配置的限额。即 12 个账号各有 20 req/60s + 30 万 tokens/60s，tag 总上限实际是 12× 配置值。

因此真正的缺口不是"按账号拆开"，而是：

1. ✅ 已修：选中即记账（`RecordRoutingAttempt` 从"请求体就绪后"提前到"渠道选中后"，controller/relay.go
   主循环与任务提交路径），收窄 check-then-act 并发超发窗口；任务路径此前完全不记账，已补上。
2. ⏳ 待做（运维）：**实测各账号真实 rpm/tpm 上限**（SenseNova 免费层零成本渐进加压探针），
   把 tag_capacity 数值贴到实测值——这是把 429 浪费压到近零、吞吐吃满的主要杠杆，属配置调参不是代码。
3. ⏳ 待做（代码，后续批次）：首次选路 token 估算为 0（`ContextKeyEstimatedTokens` 在 Distribute 后才设置），
   token 维度准入对首个尝试失效；需要在中间件阶段引入轻量估算。
4. ⏳ 可选（代码，后续批次）：Lua 原子"检查+预占"彻底消除竞态；按各账号 429 反馈自适应收敛窗口。

## 验证

- 已实施部分的回归：`go test ./service/` 全绿（含既有容量/冷却 fixture）。
- 调参后观察：`team-routing-monitor.py` 的 429 聚合与 recovered/failed 比率
（2026-09-10 基线：24h 26% 请求靠重试救回、2.6% 彻底失败、429×2125）。
