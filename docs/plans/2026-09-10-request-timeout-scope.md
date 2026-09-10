# 请求级超时应按"每次尝试"计，而非整个请求总量

状态：待实施（2026-09-10 审计提出，配置侧已临时缓解：request_timeout_seconds=1800）

## 问题

`controller/relay.go:88-89` 用 `context.WithTimeout(RequestTimeoutSeconds)` 包住整个 Relay：
所有重试尝试 + 完整流式正文共享同一个 deadline。

后果：
1. 活跃流跑到上限被 ctx 取消，客户端收到无错误事件的静默截断；此时 writer 已写字节，`shouldRetry` 必为 false，不可能换渠道。
2. 重试共享预算：前几次慢失败（如各 180s 等响应头超时）吃掉后续成功重试的时间。
3. 校验上限 1800s（`setting/operation_setting/routing_policy.go:75`），超过 30 分钟的单次请求永远无法完成。

## 方向

区分"尝试预算"与"流总量上限"：

- 方案 A（小改）：deadline 只约束到收到上游响应头为止（复用 ResponseHeaderTimeout 思路），流开始传输后解除总 deadline，流期间仍由 `STREAMING_TIMEOUT` 空闲看门狗兜底。
- 方案 B（中改）：每次渠道尝试单独 `WithTimeout`，重试时重置；需要把预算决策下沉到 `getChannel`/`relayHandler` 边界。
- 注意保持 `processChannelError` 的 ctx 取消提前返回语义（不误伤渠道熔断统计），以及 DeadlineExceeded→504+skip-retry 的改写路径仍然工作。

## 验证

- 单测：慢上游（首次尝试 2×180s 超时）+ 第三次成功长流（>900s 活跃传输）应完整交付。
- 生产回归：观察 `stream_status.end_reason=timeout` 计数（修复前 7 天 10 次，全部发生在 300s 时代）。
