# 代码审查与盈利可行性分析

> 审查日期：2026-10-27
> 结论先行：**这个仓库现在可以作为“只读行情 + 离线回测 + 纸面策略引擎”使用；没有任何证据表明它已经具备正期望，更没有达到可以实盘的程度。** 本次修复解决了大量“看起来能跑、实际连不上真实 API / 会把单位算错 / 风控管不住真实下单量”的问题，但实盘下单仍然被刻意关闭。

---

## 一、最重要的结论

1. **原版本无法正确对接真实 Polymarket。** Gamma 返回的是 JSON 数组，`outcomes`、`outcomePrices`、`clobTokenIds` 是“字符串里的 JSON”；CLOB 订单簿必须用 `token_id` 而不是 `market_id`，价格/数量是字符串；`/time` 返回裸 Unix 时间戳。原结构体会直接解析失败或得到 0 值。
2. **原版本存在会直接亏钱的单位错误。** 分析器把 0–1 的概率又除以 100；`CompareWithMarket` 把 0.58 当成 58 分；NO 方向用 `100 - price`；下单价格用 YES 价格去买 NO。修完后全仓库统一为 **0–1 小数概率/价格**。
3. **原版本风控没有约束真实订单。** `CheckTradeLimits` 检查的是它自己算出来的仓位，而各策略 `Execute` 又用另一套公式算仓位；即使检查通过，实际订单仍可能远超上限。现在改为 `ApproveOrder` 返回“风控批准的美元金额”，策略必须按该金额下单，成交后还会校验实际金额。
4. **原版本的“AI 预测”会把失败伪装成 60% YES。** LLM 返回非 JSON 时，fallback 直接给 0.6/0.5 的预测；`prediction=NO` 与 `probability=0.8` 的语义也没有定义，可能反向下注。现在强制要求 `yes_probability`，数值是唯一权威，解析失败直接报错。
5. **原版本把 API Secret 放进 HTTP Header 明文发送。** 真实 CLOB L2 认证需要 `POLY_ADDRESS / POLY_SIGNATURE / POLY_TIMESTAMP / POLY_API_KEY / POLY_PASSPHRASE`，secret 只作为 HMAC key。现在 secret 不再出网，并实现了 L2 HMAC 签名（私有接口尚未用真实密钥验证）。
6. **原版本无法实盘下单，也不应该现在实盘。** Polymarket 订单需要 EIP-712 签名（钱包私钥）、USDC allowance、salt/expiration、nonce 与成交对账。现在的 `CreateOrder` 直接返回明确错误，避免误发未签名订单。

---

## 二、本次已经修复的问题

### API / 数据层

| 问题 | 影响 | 修复 |
|---|---|---|
| Gamma 响应按 `{markets:[...]}` 解析，真实是数组 | 列表永远解析失败 | `ListMarketsResponse/ListEventsResponse` 同时接受数组和对象 |
| `outcomes/outcomePrices/clobTokenIds` 是双重编码字符串 | `Market` 解析报错或字段为空 | `Market.UnmarshalJSON` + 灵活字符串/数组解析 |
| 字段名用 `title/yes_price/last_price`，真实是 `question/outcomePrices/...` | 标题、价格、token 全为空 | 新增真实字段映射，保留兼容字段 |
| 分页用 `cursor/sort_by`，真实是 `limit/offset/order/ascending` | 参数被忽略 | 改写列表参数 |
| `string(rune(limit))` 把 100 变成字符 `d` | 所有 limit/offset/时间参数错误 | 全部换为 `strconv.Itoa/FormatInt` |
| `/book?market_id=`、`/price?market_id=` | 400 Bad Request | 改为 `token_id`，`/price` 增加 `side` |
| 订单簿价格/数量是字符串 | 反序列化失败 | `Order.UnmarshalJSON` 接受字符串/数字，并按价格排序 |
| `/time` 返回裸数字 | 解析失败 | `ServerTime` 兼容数字/对象 |
| `/price-history` 端点与参数错误 | 动量/形态策略无数据 | 改为 `/prices-history?market=...&interval=...&fidelity=...` |
| `/tick-size?market_id=` | 400 | 改为 `token_id` + `minimum_tick_size` |
| Data API 使用 `user_id`、错误路径 | 持仓/成交查询 400 | 改为 `user`，兼容数组响应与 camelCase 字段 |
| `GetBuilderVolumeTimeSeries` 参数类型错误且会越界 panic | 运行时崩溃 | 修正参数类型与查询构造 |

### 交易 / 风控 / 策略

| 问题 | 修复 |
|---|---|
| 价格单位混用 cents / 小数 | 全仓库统一 0–1 小数；回测本来就是小数 |
| 分析器用盘口不平衡/成交量“制造”最高 0.5 的假 edge | `CalculateFairValue` 改为返回市场隐含概率；没有验证过的模型就不产生交易信号 |
| YES/NO 价格错误 | NO 执行价统一为 `1 - yesPrice`，并按方向选择 token |
| 风控检查与实际下单量脱节 | `ApproveOrder` 批准美元金额，`ProcessSignal` 强制校验实际 `Size` |
| 资金占用永不释放 | 新增 `RecordSettlement`，平仓释放成本并结算 PnL |
| 日亏损/日交易次数不自动重置 | `resetDailyIfNeededLocked` 在每次风控检查时按日期重置 |
| 无最大持仓数/最大交易数 | 增加 `MaxOpenPositions`、`MaxTradeCount` 检查 |
| 策略直接调用未签名下单 | 默认 paper 模式；`CreateOrder` 硬失败；只有显式关闭 paper 才会尝试（仍会失败） |
| 订单状态未成交也记为持仓 | 只接受 `FILLED/MATCHED/PAPER_FILLED`，其他状态报错 |
| Hedge 的 PnL 永远为 0 | 新增 `UpdatePositionPrice` 标记市值；但调用方仍需在评估前更新价格 |
| 仓位 Size 语义混乱（美元/份额） | `Size` 统一为美元名义本金，新增 `Shares`；成本/均价/市值分开 |
| MARKET 单可能扫穿订单簿 | 改为带 1% 缓冲的 LIMIT，并限制在 0.99 以下 |
| 关仓只撤单、不卖币，却先删除仓位 | 实盘关仓明确拒绝；纸面关仓释放资金并结算 |

### AI / LLM

| 问题 | 修复 |
|---|---|
| 非 JSON 响应伪造 0.6 YES | 删除 fallback，解析失败直接报错 |
| `prediction` 与 `probability` 语义冲突 | 强制 `yes_probability`，P(YES) 为唯一权威，标签由概率推导 |
| Prompt 把价格写成 cents、没有要求 base rate | 改为 0–1 价格，要求基准概率、信息不足时低置信度 |
| provider/model/temperature/max_tokens/timeout 配置未生效 | `NewLLMClientWithConfig` 接入配置；按 provider 给默认 base URL |
| 无重试/限流处理 | 429/5xx 有限重试；请求/响应大小限制 |
| AI 缓存 TTL 固定 30 分钟 | 支持 `NewAIPredictorWithTTL` |

### 客户端 / 监控

| 问题 | 修复 |
|---|---|
| 构造了带 timeout 的 `http.Client` 但从未使用 | 每个 resty client 显式 `SetTimeout` |
| 只接受 200，DELETE/204 会被当失败 | 接受所有 2xx |
| POST/DELETE 自动重试可能重复下单 | 仅 GET 重试，写操作不重试 |
| API secret 作为 header 明文发送 | 移除；改为 L2 HMAC 头 |
| 监控 `GetMetrics` 浅拷贝 `LatencyStats`/map | 深拷贝 |
| P50/P95/P99 是“平均值乘系数”的假数据 | 真实样本分位数 |
| 告警数组无限增长 | 限制 1000 条 |
| 日志级别配置未生效 | 增加级别过滤 |
| `Stop` 重复调用 panic | `sync.Once` |

---

## 三、仍然需要修改（按风险排序）

> 本次追加实现了三项：EIP-712 订单签名（官方向量逐字节验证）、按订单簿逐档成交的纸面撮合、YES/NO + negRisk 篮子套利扫描器。另外落地了 logit 特征（ROC/ATR/EMA/Aroon）、Brier/log loss 校准报告、signal/fill/skip JSONL 决策日志、三重实盘开关 + token 白名单 + STOP 文件 + 心跳。下面保留的是实盘前仍然缺失的部分。

### P0：实盘前必须完成

1. **EIP-712 订单签名（已实现，真实 POST 未验证）**
   - `order_signing.go` 已实现官方 Order struct、domain、Polygon/Amoy exchange 与 negRisk exchange、rounding、salt 和确定性签名；`order_signing_test.go` 与官方 `python-order-utils` 测试向量完全一致。
   - 仍需用你的真实 API key 验证 `POST /order`、`GET /data/order/{id}` 和 `DELETE /order` 的 L2 签名串（method/path/body 必须与签名时一致），并验证 USDC/CTF allowance。
   - 建议先用 `-live-order` 的 dry-run，再用 $5 FAK 单验证全链路。

2. **完整订单生命周期与对账**
   - 当前 `LiveExecutor` 会提交、轮询 `size_matched`、在超时后撤销 GTC，并拒绝“报告 matched 但没有 matched size”的响应。
   - 仍缺 WebSocket 用户频道、部分成交的逐笔 VWAP、nonce/salt 持久化、崩溃恢复和每日对账。
   - 账户余额与持仓应以链上/官方 positions 接口为准，不能用内存里的 `Position` 当真。

3. **真实纸面撮合（已实现逐档成交，仍缺队列/延迟）**
   - `PaperExecutor` 现在按 ask/bid 逐档吃单，计算 VWAP、部分成交和手续费，不再按最优价立即全量成交。
   - 仍不模拟队列位置、网络延迟、隐藏流动性、撤单竞争；纸面结果仍然偏乐观。

4. **私有 API 认证验证**
   - 本次实现了标准 L2 HMAC 头，但没有真实 API key 验证。上线前要用小金额/只读私有接口验证签名串（method/path/body/query 的顺序和大小写必须完全一致）。
   - `POLY_ADDRESS` 必须是实际签名钱包地址，不能是任意地址。

5. **安全**
   - 私钥/API secret 只能来自环境变量或 secret manager，不能进仓库、日志、错误信息。`POLYMARKET_PRIVATE_KEY` 在配置结构中是 `json:"-"`，不会写入配置文件。
   - 检查 Git 历史是否仍有旧密钥（`STATUS.md` 已提示）；如有，立即撤销/轮换。
   - 增加 kill switch、每日对账、最大回撤自动停机、异常行情（点差突然扩大/盘口为空）拒绝交易。
   - 当前实盘 CLI 有 $25 硬上限、必须 `-live-confirm=I_UNDERSTAND`、2 秒中止窗口，默认 FAK；不要移除这些保护。

### P1：策略/研究可信度

6. **建立数据闭环**：保存每个预测（时间、市场价格、模型概率、市场规则、到期时间、结果），计算 Brier、log loss、校准曲线。没有 100–300 个已结算样本，不要谈“有效”。
7. **市场筛选**：排除接近 0/1 的已定价市场（例如 `0.02 < yes < 0.98`）、点差 > 2%、流动性不足、7 天内到期、规则模糊、`negRisk` 多结果市场（当前只支持二元）。
8. **模型校准**：LLM 只做信息抽取/情景分析，概率应由结构化模型或校准层产生；测试不同模型/温度/提示词对 Brier 的影响。
9. **费用/滑点实测**：不要假设 0 手续费。用真实成交与盘口比较，记录你的 VWAP 与最优价的差值。
10. **相关性风控**：体育、选举、宏观事件高度相关；Kelly 假设独立，实际会放大回撤。需要按事件/标签分组限制总敞口。

### P2：工程质量

- `go test -race ./...` 纳入 CI。
- 用真实响应 fixture 做契约测试（本次已加入 Gamma/Book 的核心 fixture）。
- 私有接口的签名、订单、持仓分别做集成测试（使用 mock server + 固定时钟）。
- `GetLiveVolume`、`GetTopHolders`、`GetLeaderboard` 等仍未逐一验证真实响应；不要在生产依赖。
- `UserActivity` 目前是用最多 500 条持仓/成交做的近似汇总，不是官方统计。
- 配置中的 `Environment` 未真正切换 base URL（目前恒为生产）；如需 staging，要加 URL 覆盖。

---

## 四、为什么当前策略本身不构成“赚钱能力”

1. **分析器的“公允价”不是预测模型。** 原实现是 `市场价格 + 成交量趋势调整 + 订单簿不平衡调整 + 流动性调整`，没有任何新信息；订单簿不平衡更可能是**逆向选择**。本次已让 `CalculateFairValue` 只返回市场隐含概率，不再凭盘口“制造” edge。
2. **AI 没有信息优势。** 没有新闻检索、没有历史基准率数据库、没有规则解析，只靠标题和市场价让 LLM 猜概率。LLM 的概率通常未校准，且会受市场价锚定。
3. **没有可执行的信号到订单链路。** 原 CLI 不扫描、不下单；现在只读行情可用，但仍没有“筛选 → 预测 → 校准 → 风控 → 签名 → 对账”的闭环。
4. **成本会吃掉小 edge。** 以本次实测的 `Xi Jinping out before 2027` 为例：中间价 0.0365，最优买/卖 0.036/0.037，**1 个 tick 就是约 2.7% 的本金成本**。如果你的模型 edge 只有 2–3%，交易就是给做市商送钱。
5. **长尾/热门偏差。** 高成交量市场往往已经非常有效（0.0005/0.9995），低成交量市场又可能流动性不足/规则模糊。真正可赚的市场通常在中间，但需要筛选和耐心。
6. **分辨率风险。** 规则解释、UMA 争议、结算延迟都会让“看起来赢了”的仓位无法及时兑现。

---

## 五、可以真正尝试的赚钱路径（按现实可行性排序）

> 下面不是“保证盈利”，而是“有明确 edge 来源”的方向。任何一条都必须先用纸面/小资金验证。

### 1. 结构性套利（最现实，但速度要求高）

- **多结果事件 YES 组合**：同一事件的所有互斥结果 YES 价格之和 < 1（扣除费用后）时，买入全部 YES，到期必有一份兑付 1，理论无风险。反之，若 YES 之和 > 1，可考虑买入 NO 组合（需确认负风险市场机制）。
- **二元市场 YES + NO < 1**：同时买入 YES 和 NO，到期必得 1。现实中做市商通常已抹平，机会短暂。
- **跨市场对冲**：Polymarket 与 Kalshi/体育博彩/加密期权对同一事件定价不同，两边对冲。需要其他平台账户、合规与资金调度。
- **实现要点**：扫描全盘口、计算含费 edge、原子/快速执行、失败回滚、资金占用与结算周期。先做**只读扫描器**，记录机会频率和持续时间，再决定是否值得做执行。

### 2. 做市 / 流动性提供

- 在点差较宽、成交量足够的市场双边挂单，赚取 spread 和潜在流动性激励。
- 风险是库存与逆向选择：需要快速撤单/改单、库存上限、波动保护、对 toxic flow 的识别。
- 这通常需要低延迟基础设施，不适合“LLM 定时扫描”型机器人。

### 3. 事件驱动 / 新闻延迟

- 选举、体育伤病、法院裁决、ETF 审批、公司财报等事件，市场更新有秒级到分钟级延迟。
- 需要快速、可靠的数据源与预写规则；LLM 可用于解析新闻，但**执行速度**才是 edge。
- 必须有“消息真实性”校验，避免假新闻/标题党。

### 4. 细分领域基本面预测

- 选择你有领域知识、参与者少、流动性尚可的市场（小众体育、特定政策、链上事件）。
- 用结构化基准率模型 + 人工判断，LLM 只做资料整理；持有到结算，减少交易频率和点差损耗。
- 关键是**校准**：只在模型历史 Brier 显著优于市场隐含概率时下注。

### 5. 跟踪聪明钱（有延迟地跟单）

- 用 Data API 找持续盈利的钱包，分析其持仓变化；跟单的 edge 来自信息扩散延迟。
- 风险：跟单拥挤、对方改变策略、你看到的是滞后持仓、对方可能是在对冲。
- 适合作为信号来源，不适合无脑复制。

### 6. 统计套利 / 相关市场

- 同一事件不同日期/不同阈值（例如 BTC 12 月 vs 1 月）之间，价格应满足单调/无套利关系；偏离时做多低估、做空高估。
- 需要处理相关性、结算规则差异和腿的风险。

### 不建议作为核心策略

- **无信息优势的高频动量**：点差和手续费会吃掉一切。
- **纯 LLM 猜概率**：没有校准和外部数据时，长期期望大概率为负。
- **追涨杀跌/盘口不平衡**：很容易成为知情交易者的对手盘。
- **重仓近 0/1 的长尾**：收益有限、尾部风险巨大；需要严格的仓位和相关性限制。

---

## 六、建议的实施路线（从今天开始）

### 阶段 0：只读数据（1–3 天）

```powershell
go test ./...
go run ./cmd -markets 20
go run ./cmd -market 559651
go run ./cmd -wallet 0x...
```

- 把 `-markets` 结果按 `0.05 < yes < 0.95`、点差、到期时间、流动性过滤。
- 记录盘口快照到本地（SQLite/JSONL），观察机会持续多久。

### 阶段 1：研究与回测（1–2 周）

- 收集已结算市场 + 历史价格 + 规则文本。
- 实现至少一个**可解释**的基准率模型（例如体育 Elo、选举民调、宏观 nowcast）。
- 用 `BacktestCSV` 或新写的组合回测，加入手续费、滑点、资金占用、相关性。
- 目标：Brier/log loss 显著优于“直接用市场价”，且扣除成本后 ROI > 0。

### 阶段 2：纸面撮合（基础版已完成）

- `PaperExecutor` 已按订单簿逐档成交并记录滑点；仍需队列位置和延迟模型。
- 对比纸面 PnL 与回测 PnL；差异过大说明回测过于乐观。

### 阶段 3：小额实盘（执行层已实现，待真实凭证验证）

- EIP-712 签名、`POST /order`、轮询 `size_matched`、超时撤单和 `-live-cancel` 已实现。
- 先用 dry-run，再单笔 $5–$20 验证下单/撤单/成交/结算全链路。
- 实盘滑点与纸面偏差超过预期就停机。

### 阶段 4：扩张

- 只有在 100+ 笔实盘、校准和收益都达标后，才考虑提高仓位。
- 按事件/标签限制相关性敞口，保留现金缓冲。

---

## 七、关键指标（不要只看收益率）

- **Brier score / log loss**：概率预测是否真的比市场好。
- **校准曲线**：预测 60% 的事件实际发生率是否接近 60%。
- **扣费后 ROI、Sharpe、最大回撤、Profit Factor**。
- **成交滑点**：实际 VWAP 与决策时最优价的差。
- **机会命中率/持续时间**：套利机会是否值得建设执行基础设施。
- **样本量**：少于 100 笔的“稳定盈利”基本是噪声。

---

## 八、代码层面的下一步清单

1. **已完成**：`order_signing.go` 实现 EIP-712 typed data + 官方测试向量（普通与 negRisk）；订单结构包含 tokenID、price、size、side、feeRateBps、nonce、expiration、salt。剩余：用真实 API key 验证 POST/cancel 与 allowance。
2. **部分完成**：`OrderSigner` + `PaperExecutor` + `LiveExecutor` 已统一到 `Executor` 接口；剩余：WebSocket 用户频道与逐笔成交 VWAP 对账。
3. **已完成**：`WalkOrderBook` 按盘口逐档计算 VWAP、可成交数量、部分成交和手续费。
4. **进行中**：私有 API 签名已实现，仍缺固定时钟/mock server 的正式集成测试与真实凭证验证。
5. WebSocket 行情/用户频道：盘口增量、成交回报、断线重连、心跳、序列号校验。
6. SQLite 持久化：signals、orders、fills、positions、settlements、prediction outcomes；崩溃恢复与幂等。
7. 风控增强：事件相关性分组、单事件最大敞口、点差/深度保护、行情陈旧保护、每日对账、kill switch。
8. 市场筛选器：价格带、到期时间、点差、深度、规则完整度、排除多结果/负风险（或单独支持）。
9. 校准流水线：保存每次预测与结果，定期输出 Brier/可靠性图；只有达标模型才能进入交易。
10. CI：`go test -race`、`go vet`、真实响应 fixture、lint、secret 扫描。

---

## 九、使用方式（当前版本）

```powershell
# 测试
go test ./...
go vet ./...

# 只读行情（不需要 API key）
go run ./cmd -markets 20
go run ./cmd -market 559651
go run ./cmd -wallet 0x91430CaD2d3975766499717fA0D66A78D814E5c5

# 套利机会扫描（只读，可循环写 JSONL）
go run ./cmd -arb-scan 5 -arb-min-edge 0.005
go run ./cmd -arb-scan 20 -arb-interval 60 -arb-out .\opportunities.jsonl

# 概率校准
go run ./cmd -calibrate .\signals.csv -calibration-bins 10

# 历史数据 → 样本 → 双基线校准
go run ./cmd -collect-resolved 100
go run ./cmd -build-samples

# 当前实测结论：320 市场、309 个可采样市场、883 条决策；
# 事件分组 holdout 的 Bootstrap 区间跨 0，结论 NO_EDGE。
# 详见 RESEARCH.md。

# 实盘签名 dry-run（不发送）
go run ./cmd -live-order <TOKEN_ID> -live-shares 5 -live-price 0.10 -live-max-usd 5

# 离线回测（必须显式给费用/滑点）
go run ./cmd -backtest .\signals.csv -capital 10000 -stake 100 -min-edge 0.05 -fee-bps 200 -slippage-bps 100

# 状态
go run ./cmd -status
```

实盘默认关闭；`-live-dry-run=false` 还需要 `trading.mode=live`、`trading.enabled=true` 和 `-live-confirm=I_UNDERSTAND`，单笔硬上限 $25。详见 [LIVE_TRADING.md](LIVE_TRADING.md)。

---

## 十、最后的风险提示

- Polymarket 在部分司法辖区受限；使用前确认当地法律与平台条款。
- 预测市场可能出现规则争议、结算延迟、流动性瞬间消失。
- 任何“稳定高收益”宣传都不可信；请用可承受损失的资金，并先做纸面验证。
- 本次修复让行情读取和纸面风控可信了很多，但**没有创造 edge**。赚钱来自信息、结构、速度或风险承担，不来自“接上了 API”。

---
