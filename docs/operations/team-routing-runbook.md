# new-api 团队路由运维规范

## 授权与不变量

2026-09-06 用户批准原生设置优先，必要时修改代码、UI并部署定制版；旧服务器说明中的“禁止定制构建”由此更新。管理员是用户本人，其他账户是团队成员。天翼云必须等全部修复、验证、部署和监测准备完成后才开启。

| 分组 | 顺序 | 权限 |
|---|---|---|
| default | SenseNova | 仅 deepseek-v4-flash、sensenova-6.8-flash-lite |
| plus | SenseNova → 火山 → Kimi | 保留现有 Plus 白名单；禁止天翼 |
| dev | SenseNova → 火山 → Kimi | 保留现有 Dev 白名单；禁止天翼 |
| vip | Kimi → 火山 → SenseNova → 天翼 | 天翼仅作为最后兜底 |

Plus 的 Kimi 订阅白名单是 `kimi-for-coding`、`kimi-k3-256k`。Plus 火山白名单是 `glm-5.3-flash`、`glm-5.3-256k`、`glm-5.2-256k`、`deepseek-v4-flash`、`kimi-k3-256k`。SenseNova 旗舰白名单为 `kimi-k3`/`k3`、`glm-5.2`、`deepseek-v4-pro`。模型名属于权限边界，别名须在每一条允许回退的渠道中显式登记并正确映射，不能悄悄替换模型。

## 配置入口和行为

后台“系统设置 → 模型设置 → 路由策略”（`/system-settings/models/routing-policy`）编辑单个原子 JSON 配置 `RoutingPolicy`。渠道列表原生“标签”使用 `sensenova`、`volcengine`、`kimi`、`ctyun`；名称仅存在部署配置，应用代码不识别这些特殊名称。

- 已配置分组只在其原生 group/model 权限内选取列表中的标签；未配置分组保留原版行为。
- 同供应商保持现有渠道优先级/权重选择；一次请求不重复尝试同一渠道。2026-09-07 用户确认改为 `max_attempts_per_tag=0`：遍历同供应商全部当前可用账号后才回退。`max_total_attempts=32` 单独约束整次请求，覆盖当前最多17个相关账号；不再为了预留下游尝试次数提前跳过免费账号。次数或时限耗尽时结束请求。原生 `RetryTimes=7` 仅用于未启用独立总预算的请求。
- 同一请求只向后回退。下一次请求重新从最高健康标签开始，亲和只能在该标签内选择，不会长期粘住低优先级按量渠道。
- 429 冷却 60 秒，匹配额度关键词冷却 3600 秒。关键词区分额度耗尽和鉴权失效；额度关键词从永久禁用列表移到临时冷却列表，鉴权禁用和原生恢复探测保留。
- 冷却按渠道和映射后的上游模型隔离，别名共享冷却；进程内状态，重启清空。当前为单实例，不宣称多个实例共享冷却或不同渠道一定属于不同额度池。
- 300 秒限制整次请求，180 秒原生响应头超时保留。客户端取消传到通用 OpenAI/Claude 上游，取消/到期后不继续网关重试。客户端自行重试仍需单独管理。
- 部分流用量仍按原记账规则结算；超时/中断被标为失败，不刷新亲和。不把消费记录或 HTTP 200 单独当作完整成功，也不自动退款来掩盖上游消耗。

## 缓存计价口径

用户明确选择：保留现有输入/输出价格，按模型官方公开折扣补齐已核实的缓存价格。部署脚本仅补充缺失的 CacheRatio，不覆盖已存在的管理员值；从源版本内置表保留其他模型默认配置。

依据见 [价格证据](team-pricing-evidence.md)，本次配置见 [缓存折扣](team-cache-discounts.json)。使用官方 USD 价格计算折扣；Doubao 公布 CNY 比率为 0.2，比例无需换汇。此处是团队内部记账，不能称为火山AFP、Kimi订阅积分或天翼实际账单。

没有独立依据的别名（K3-256K、GLM-256K、高速编码）和缓存写入附加费，不猜测优惠。有效默认价会明确显示“默认倍率”，不会继续隐藏成空白。legacy 未配默认读倍率1、写倍率1.25不等于供应商已证实的价格；后续获得证据再配置。表达式计价保持独立，不混入legacy默认值。

## 部署和回滚

1. 保持天翼渠道手动关闭。确认目标分支和镜像唯一版本，禁用每晚自动追RC。
2. 运行 Go 完整测试、relaykit独立构建、前端检查及真实 SQLite/MySQL/PostgreSQL 路由矩阵，完整应用只连接模拟上游验证分组和回退。
3. 生产端禁止复制凭据。自动审批曾拒绝额外复制完整数据库/.env及在生产PG创建测试角色；改用本机独立环境。原每日备份保留，本次无schema/driver/migration改动。
4. `python3 ops/team-routing-configure.py --database new-api` 预览；`--apply` 保存仅含渠道元数据与相关设置的600权限快照后事务更新，默认保持天翼关闭。并发管理员修改会导致事务中止。
5. 使用独立 `docker-compose.override.yml` 镜像覆盖文件启动已验证版本，不修改原 `.env`。原镜像保留。单实例切换可能短暂中断，不宣称零停机。
6. 核对版本、健康、UI、白名单、价格、真实被动流量和监测工具，最后才执行 `--apply --enable-metered`。
7. 回滚程序：用原 Compose 文件重建原官方应用（不要加载自定义覆盖文件）。需要恢复配置时只恢复本次快照中变动的字段和选项；不要恢复整库覆盖新消费记录/用户余额。紧急情况下先关闭天翼。

## 持续监测

在主机上运行 `python3 /opt/new-api/ops/team-routing-monitor.py --minutes 60`；脚本只做只读SQL和访问日志聚合，不请求模型、不读取请求正文或导出凭据。Codex定时任务在本任务内跟进；主机或运行环境不可用时报告无法检查，不假装监测仍在运行。

比较基线与后续窗口：

- HTTP 429/5xx、流式 `other.stream_status.status/end_reason`、缺少流状态的消费数。
- 按 request_id 关联的错误尝试和最终消费；一个请求可能有多个错误，不能直接计算日志错误条数比例。
- 各分组/模型/供应商消费、网关记录的P95耗时和入口HTTP P95、缓存读取Token、内部配额。
- 非VIP进入天翼、default出现额外模型、出现未列入分组策略的标签、重复渠道/向前回退，均应优先检查。
- 天翼消费增长须按内部记账与实际供应商账单分开解释；当前没有凭据或价格证据支持宣称自动约束真实供应商月账单。

无明显变化时保持安静；越权、可用性明显下降、持续限流、流中断恶化、新错误或按量消耗异常增长时通知。通知需给出窗口、样本量、前后比较和证据边界。监测本身不授权自动修改价格、权限、供应商账户或执行有费用的探测。

## 2026-09-07 全账号遍历修正

旧模式每个供应商最多尝试3个账号，导致12个SenseNova账号仅试3个就回退。现有UI允许每标签次数设0（全部）或1–256，整次请求上限设0（继承原生重试）或1–1024。生产选择全部/32，保留300秒总时限、60秒限流冷却和原分组权限。已锁定原始渠道的任务保留原生重试语义，不将锁定任务作为可自由切换的账号池。


## 2026-09-08 协议能力路由

渠道设置 `protocol_routing` 为可选原子配置。未启用的渠道保持原行为；启用后，`defaults` 定义入口协议与已验证端点，`models` 按映射后的上游模型完整覆盖默认策略。路径附加在已有上游 Base URL；预置 Coding Plan 按目标协议选择对应 Base URL。当前认证适配范围为 OpenAI、Anthropic、Moonshot、VolcEngine，其他类型不能启用。

每个端点声明 `openai`、`claude` 或 `openai_responses`，以及 stream/tools/parallel_tools/images/files/audio/video/structured_output/reasoning/hosted_tools 等能力和验证日期。未知能力不默认开放。入口支持不代表目标模型支持全部参数；例如实际原生测试确认 Kimi 开启 thinking 时不能指定强制 tool_choice，关闭 thinking 后两个原生协议均能完成工具调用。是否改变思考设置由管理员在 UI 中显式决定。

`loss_policy` 默认 safe，跨协议在发送前拒绝已知不可保留的字段或工具历史；strict 更严格，allow 是明确允许损耗的选择。stateful/background 在第一阶段始终拒绝，不模拟 previous_response_id/会话存储。Messages count_tokens 路由仍未启用，本次不将其伪装成可用。

选择顺序仍是分组供应商顺序；同账号优先原生协议，转换不会跨越白名单或替换模型。不兼容候选只记入 `admin_info.protocol_skips`，不消耗上游尝试次数；已发送流式字节后禁止切换供应商。流式失败发出客户端格式的错误事件，保留已报告的部分用量结算，不能在失败流后补成功完成事件。

`account_resource` 为非秘密账号资源标识；同一账号的渠道别名使用同一个值。`quota_scope` 可为 model（默认，使用链式映射后的上游模型）或 account。共享冷却和本次请求排除均跨别名生效，不重新尝试同一个资源。未配置标识时沿用渠道身份；多 Key 渠道仍以渠道为资源，不声称已支持 Key 级独立账号轮换。

现有调用日志显示转换链和最终协议；管理员信息增加 protocol_route/source/target/path/loss_policy 与协议候选跳过原因。监测脚本增加 protocol_routes 聚合。SQL 初始配置可用 `ops/protocol-routing-configure.py`，默认只预览；`--apply` 在乐观并发检查的事务内仅更新协议字段，0600 快照仅保存原协议字段。SQL 更新后须正常重启/刷新渠道缓存。回滚恢复快照中的协议字段和原镜像，不恢复整库，也不修改价格或渠道启停。


### 原生上下文编辑与服务端会话状态

`context_management` 表示请求内的上下文编辑指令，不能一概视为依赖网关会话存储。`context_editing` 能力仅在已验证的原生 Messages/Responses 路径保留整个字段（包括空对象）；安全/严格跨协议转换仍拒绝丢弃它，原生 Chat 的 DTO 不支持该字段，所以即使误声明能力也拒绝。`previous_response_id`、conversation/container 引用和 background 仍不由第一阶段模拟。发现用户原生 Messages 被旧的全局 stateful 检查误拦后，曾暂时禁用协议路由恢复原行为，再应用修正。

实测 SenseNova、火山现有 Base URL 下的 `/v1/messages` 也能原生接受上下文编辑请求；初始配置为这些渠道补充已验证 Messages 端点，避免不必要的 Messages→Chat 损耗。Kimi 的两个原生格式已验证，仍保留其自身思考/强制工具参数限制。

### 能力检查失败的诊断

`no verified protocol endpoint supports the request capabilities` 表示候选端点在请求发送前全部被能力检查排除，不是供应商返回的错误。诊断补丁会按端点协议列出 `missing capabilities`；没有已验证端点时明确返回 `no endpoints are verified`。输出只含已验证的协议标识和程序内的能力名，不含请求内容、工具描述、模型值或管理员端点路径。

排查时关联客户端错误中的 request_id 与应用日志，再核对客户端所选模型及映射后的模型策略。区分配置遗漏、能力识别错误和上游确实不支持；只有实际核实支持后才在渠道 UI 的默认策略或模型覆盖中声明能力。不要直接勾选所有能力，也不要静默删除图片、工具或上下文以换取 HTTP 200。`hosted_tools` 指上游托管工具能力；它不证明请求一定开启了网页搜索，须继续核对实际工具类型。

2026-09-08 16:30/16:31（UTC+8）的 Codex Desktop Windows `/v1/responses` 请求在旧的通用错误处被拦截。旧日志未保留能力明细，不能追溯确切缺失项；须用补丁后的客户端重试确认。诊断补丁本身不代表这个客户端的兼容性问题已经修复。

### kimi-k3 原生 Responses 修正（2026-09-08）

用户确认模型为 `kimi-k3`、缺失项为 `hosted_tools`。同版本 Codex CLI 0.153.4 的本地离线请求形状检查发现 function、namespace 与 web_search 并存。namespace 是客户端工具分组，不能自动归为托管执行；网关现在递归检查其中的工具声明，并保留深度限制和跨格式损耗检查。

按实际供应商入口登记原生接口，不依据 new-api 旧适配器的 `not implemented` 推断供应商能力。此次修改现有14条渠道的协议配置，未修改用户/分组/模型授权、供应商顺序、价格或渠道启停。

| 供应商与模型 | 原生 Responses 路径（附加在当前 Base URL） | 本次验证与配置边界 |
| --- | --- | --- |
| Kimi 订阅四个现有模型 | `/responses` | 普通函数调用通过；四个型号在原生 Responses 和 Messages 下的联网搜索均验证成功。两个原生端点的默认策略及已有模型覆盖均登记 hosted_tools。K3 和 kimi-for-coding 的 namespace 调用另有网关验证。 |
| 火山 `kimi-k3` | `/v3/responses` | 基础请求、SSE、普通函数调用通过；此 Responses 路径的联网与 namespace 探针未产生相应工具调用。该模型及 kimi-k3-256k 的原生 `/v1/messages` 则实际执行了 WebSearch，分别在模型覆盖中登记 Messages hosted_tools。 |
| 天翼 `kimi-k3` | `/v1/responses` | 非流式返回合法的输出长度截断响应和用量；联网 SSE 探针超时，未声明 stream 或 hosted_tools。仍仅受原 VIP 权限控制。 |
| SenseNova | `/v1/responses` | 当前入口返回 NOT_FOUND。对 kimi-k3 Messages 流式 WebSearch，12个独立Key账号均未返回有效搜索SSE事件，暂不声明这项能力；保留普通 Chat/Messages 路径，不据此断言未来或其他入口永不支持。 |

[Kimi 官方 Codex 接入文档](https://www.kimi.com/code/docs/third-party-tools/codex.html)明确说明原生 Responses 和联网搜索。实测 `web_search` 可执行并正常完成；`web_search_preview` 以及测试中的强制 tool_choice 组合返回上游400，未静默改写工具或关闭用户功能。

排查能力时区分协议入口、具体模型、工具类型和参数组合。先核实兼容的原生路径，再考虑转换；原生路径也不意味着该模型实现协议中的所有工具。后续监测结合 HTTP、stream_status、protocol_route 和实际工具调用，不能把工具声明存在或 HTTP 200 当作功能已执行。

用户后续报告的 Claude WebSearch 错误来自 `/v1/messages`。仅登记 Responses 搜索不足以支持 Messages 搜索；原错误文本展示了最后一个候选端点的缺失项，不能把其中的 stream/tools 外推为所有供应商都缺这些能力。Kimi 原生 Messages 的 `web_search_20250305` 已实际返回 server_tool_use、搜索结果及正常 message_stop，现已补齐配置。Kimi 四型号×两种原生格式的搜索均已实测；火山 kimi-k3 返回正常搜索结果，kimi-k3-256k 返回10条结果后达到测试输出限额，不能把后者称为完整最终回答。修复使用既有UI可配置字段，无需继续改代码或重启应用。

### 搜索计数与附加费

后续账单核查发现 Kimi 的 Messages 响应省略 `usage.server_tool_use`，原路径因此漏计搜索附加费；Responses 路径按实际输出项计数。`1e4b491ed36f` 修正计数，保持 `tool_price_setting.prices` 的现有单价及模型输入/输出/缓存价格不变。

上游最终统计对象优先，包括明确的0。没有该统计时，按完整且成功的 web_search_tool_result 块去重统计 tool_use_id；流式须收到对应 content_block_stop，非流式按完整响应统计。一次搜索的多条结果只计一次，错误结果、未完成块不增加搜索费用。每个上游尝试开始前清空临时计数，避免重试串账；已观察到的实际计数替代 search-preview 模型的隐式一次搜索假设，避免重复附加。历史消费记录不重算。

监测脚本新增 `search_tool_charges`，按分组、供应商、模型、入口/目标协议、工具和配置单价汇总已计费搜索次数及异常流数量。`priced_calls` 仅反映账单中的收费项，缺少该项不证明没有搜索（例如管理员将工具价格设0）；配置单价不是供应商实际账单。此部署监测器使用既有 PostgreSQL 只读连接，未修改应用数据库模型、迁移或驱动。


### 共享协议模板与批量验证

入口：渠道管理（`/channels`）→ 更多 → 共享协议配置档。本文将配置档称为模板。账号渠道引用供应商的具体产品模板，模板保留原生 Chat Completions、Messages、Responses 端点和能力；模型或渠道只保存差异。模板不能授予分组或模型权限，也不会改变供应商优先顺序、账号冷却或计价。原有内联配置继续支持。

1. 在模板页编辑并保存目录；使用版本校验，过期编辑会提示刷新。模板默认策略完整，模型差异可只写 `endpoint_overrides`；功能的显式 `false` 表示移除，缺省表示继承。
2. 在绑定页选择产品和账号，预览后确认。批量绑定清除重复的本地策略并保留账号资源标识等其他设置；解除绑定会复制当时生效的策略。
3. 批量验证默认选代表账号；需要比较十二个账号时明确全选账号。勾选所需协议与检查，创建报告后再运行。联网检查需单独启用，会消耗上游额度；不扣团队网关账本，不改变渠道健康。
4. 每次最多128项、并发2、单项45秒、输出上限1024、响应上限1MiB。关闭界面会停止后续批次；报告保留，可重新打开继续。中断的已发请求标记未知，不自动重放。
5. HTTP200不等于验证通过。函数须实际调用，联网须出现有效搜索结果，流式须正常终止；429/5xx/超时/截断为未知，4xx只说明该测试组合被拒绝。只允许预览并应用通过结果；失败不会自动删除已有能力，渠道或模板变更后旧证据不可直接应用。服务器保留最近20份有限大小的报告，不保存Key、原始请求或响应正文。

新增供应商产品时配置一份模板并绑定账号；新模型通常继承模板，仅登记确实不同的接口或能力。协议定义的客户端工具由统一分类器识别，原生托管工具仍取决于供应商实际实现。新工具语义或有损跨格式转换不能靠勾选虚构支持。持续监测只读观察使用日志、能力拒绝、流完整性和搜索计费；不自动运行真实探测或调整配置。

### Responses 完成与客户端断开

原生 Responses 验证合法终止事件、检查实际写出并保留用量后，应立即结束扫描，无需等待上游关闭连接。成功终止写出与客户端取消同时发生时，以已交付的协议终止为准；终止前取消、无效终止、写出失败仍为错误。转换路径结束上游扫描后仍须完成下游格式的终止输出，不得直接套用原生交付确认。

`client_gone / context canceled` 只说明网关观察到下游取消，不能单独判断用户主动停止、客户端解析失败或终止后正常断开。排查时结合协议方向、终止证据和客户端实际错误。历史日志不改写；新旧成功率比较须标明这次终止判定修复，不能将减少的误报全部解释为供应商可靠性改善。

### Messages completion and Codex auto-review compatibility (2026-09-09)

`codex-auto-review` is the authorized alias of `deepseek-v4-flash` on the original SenseNova free channels and Fire6. It uses the copied target input/output/cache prices; it is not a third underlying default model. The current alias guard baseline is `/opt/ops-backups/model-alias-codex-auto-review/guard-after.jsonl`.

The owner explicitly declined `kimi-k3` → `kimi-k3-256k` substitution. Plus `kimi-k3` currently has only SenseNova same-name candidates. A cooldown-exhausted request can therefore legitimately return503 even though other suppliers offer256k variants. Do not add aliases, lift white lists, or change models to hide this condition.

Claude Messages scanners now stop at `message_stop`. Only native terminal data whose write/flush succeeded can win a concurrent client cancellation. Converted streams still need checked downstream finalization. Early cancellation, empty EOF, missing `message_stop`, and write errors remain failures; preserve reported partial usage. This does not repair supplier-originated empty200 streams.

Codex0.153.4 auto-review uses Responses Lite `additional_tools` with namespaced custom/function tools and JSON Schema. Offline reproduction identifies this input shape; original team bodies are not retained. Do not drop these declarations or claim a free-text test proves approval compatibility. Diagnostics identify a canonical content path and known type without logging arbitrary type values, tool arguments, or request bodies.

SenseNova and Fire accepted tested DeepSeek V4 Flash Chat `json_schema` requests and returned valid schema output plus completed streams. The shared profiles declare `structured_output` only for that model's Chat endpoint. A valid sample does not prove every JSON Schema keyword or strict enforcement. Fire's native Responses schema test succeeded, but required namespace/custom tool probes returned text without tool calls, so those capabilities were not declared and native routing was not enabled on that evidence.

Current evidence and configuration snapshots live in `/opt/ops-backups/protocol-compat-fix/`. Continue read-only monitoring. Do not run real probes or mutate configuration from the heartbeat.
