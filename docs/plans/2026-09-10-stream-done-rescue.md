# converted 流 Done 救援条件放宽

状态：待实施（2026-09-10 审计第 11 条，用户确认顺手修）

## 问题

`relay/helper/stream_scanner.go:325-330` 的"handler 完成后 scanner 错误不记"救援仅覆盖
`handlerCompleted=true`（即 `DoneAfterDelivery()`）路径。converted 流（Claude 上游 → OpenAI/Gemini 客户端）
在 message_stop 时走 plain `Done()`（relay/channel/claude/relay-claude.go:406），completed=false 无救援：
若上游在 message_stop 投递后、handler 出队前的微秒窗口内 RST/异常断连，ScannerErr 抢赢 endOnce
→ 内容已完整交付却被追加 502。native 路径不受影响（DoneAfterDelivery 置 completed=true）。

## 修复

救援条件放宽为 `handlerCompleted || EndReason==Done`（plain Done 已消费 endOnce 即代表
"终止事件已收到且已入队交付"，此后的传输错误不改变结果完整性）。

## 验证

- 单测：模拟 converted 流 message_stop 后立即 RST 上游连接，断言不再返回 502 且 IsSuccessful()=true；
- 回归：native 路径现有测试（TestClaudeTerminalFinishesBeforeUpstreamEOF 等）保持绿。
