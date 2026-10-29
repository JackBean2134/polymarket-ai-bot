# Polymarket AI Bot (experimental research)

This repository is an experimental Go codebase, not a proven profitable strategy or a production-ready trading bot. It supports **offline backtests, read-only market/arbitrage research, book-walking paper fills, and an explicitly gated live-order path**. Live ordering is off by default and must be enabled with explicit flags, credentials and confirmation; it has a hard $25 per-order cap. The strategy/API packages are not evidence of positive expected returns.

The supported workflows are:
1. offline CSV backtest over timestamped model predictions and resolved outcomes;
2. read-only Gamma/CLOB market snapshots and Data API wallet inspection;
3. read-only YES/NO and negRisk basket arbitrage scanning with JSONL logging;
4. paper execution that walks visible order-book levels (VWAP, partial fills);
5. EIP-712 signed single orders with a hard USD cap, dry-run by default.

Backtest results are only as reliable as the data and assumptions supplied; they do not predict future returns. See [REVIEW.md](REVIEW.md) for the code review and profitability analysis, and [LIVE_TRADING.md](LIVE_TRADING.md) before touching a real wallet.

## Requirements

- Go 1.21+
- No API keys are needed for tests, backtests, `-markets`, `-market`, or `-wallet`

## Build and tests

From the repository root:

```powershell
go test ./...
go run ./cmd -h
```

## Read-only market and arbitrage research

These commands only read public data. They never sign or submit orders.

```powershell
# Top active binary markets by 24h volume, filtered by liquidity
go run ./cmd -markets 20

# One market: Gamma metadata + YES order book, midpoint, logit features
go run ./cmd -market 559651

# Calibration report (Brier, log loss, reliability buckets)
go run ./cmd -calibrate .\signals.csv -calibration-bins 10

# Any wallet: current positions and recent trades (Data API)
go run ./cmd -wallet 0x91430CaD2d3975766499717fA0D66A78D814E5c5

# Scan 5 top events for YES/NO and negRisk basket arbitrage; append JSONL
go run ./cmd -arb-scan 5 -arb-min-edge 0.005 -arb-out .\opportunities.jsonl

# Repeat every 60 seconds until Ctrl+C (frequency study)
go run ./cmd -arb-scan 20 -arb-interval 60 -arb-out .\opportunities.jsonl

# Bound the per-event work (large sports events can have hundreds of markets)
go run ./cmd -arb-scan 10 -arb-max-markets 25
```

Prices are printed as decimals in `[0, 1]` (Polymarket quotes `0.58`, not `58` cents). `-markets` defaults to excluding near-resolved markets; add `-min-price`, `-max-price`, and `-min-days` for your own universe. The arbitrage scanner uses best asks, visible size and the market's `feeSchedule`; it is a measurement tool, not an execution guarantee.

### Arbitrage definitions

- `BINARY_YES_NO`: buy YES and NO in the same market; payout is $1 per basket. Opportunity when `yesAsk + noAsk + fees < 1`.
- `NEGRISK_YES`: in a negRisk event with N mutually exclusive markets, buy YES on all N; exactly one pays $1. Opportunity when `sum(yesAsk) + fees < 1`.
- `NEGRISK_NO`: buy NO on all N; N-1 pay $1. Opportunity when `sum(noAsk) + fees < N-1`.

The scanner requires every market in the event to be open and fetchable before reporting a basket. It reports `max_baskets` from top-of-book size and per-basket net edge after the configured fee estimate. Use JSONL output to measure how often and how long opportunities survive before building execution.

### Fees

Polymarket charges takers

```text
fee = shares × rate × p × (1 - p)
```

where `rate` comes from the market's `feeSchedule.rate` (Crypto 0.07, Sports 0.05, Politics/Finance/Tech/Mentions 0.04, Economics/Culture/Weather/Other 0.05, Geopolitics and World events free). Makers pay nothing. The formula is symmetric around 50¢ and is non-linear in price, so the scanner charges it per price level rather than on the VWAP.

`takerBaseFee` is a legacy field and is **not** the rate: a sports market reports `takerBaseFee: 1000` while its real rate is `0.05`. Treating 1000 as 10% of notional overstated fees by roughly 50× and rejected almost every real basket. When a payload carries no `feeSchedule`, the scanner falls back to the category table above; it never invents a rate from the legacy field.

## Supervised paper autopilot

`-autopilot N` runs N cycles of a narrow autonomous loop. It is **paper only**: it reads public order books, simulates fills against visible levels and never signs or submits an order. It exists to measure whether structural arbitrage is worth building execution for, not to promise returns.

```powershell
# 5 cycles, 2 minutes apart, $100 budget, $25 per basket, 2c per $1 basket edge floor
go run ./cmd -autopilot 5 -autopilot-interval 120 -autopilot-budget 100 `
  -autopilot-max-usd 25 -autopilot-min-edge 0.02 -autopilot-out .\autopilot.jsonl

# Stop it from another window (kill switch is checked before every cycle)
New-Item .\STOP
```

Hard brakes, all enforced in code:

| Brake | Behaviour |
|---|---|
| `MaxTotalUSD` / `MaxPerEventUSD` | refuses a basket that would exceed the run or event budget |
| `MaxPerBasketUSD` | caps the cash of one basket |
| `EventCooldown` | the same event cannot be re-bought immediately |
| `MaxUnhedgedUSD` | halts once unhedged (partial) leg notional reaches this amount; partials are counted as a total loss |
| `MaxConsecutiveFailures` | halts after repeated scan or execution failures |
| `StopFile` | any cycle after the file exists stops the run |
| Paper-only executor | `NewAutopilot` rejects any executor whose `IsPaper()` is false |

A basket is only recorded when every leg fills completely and the realised VWAP still clears the edge floor. `locked_profit_usd` is locked-in edge, not cash: it is only realised when the market settles, and settlement/rule risk is not modelled. See [RESEARCH.md](RESEARCH.md) for why momentum and order-book heuristics are excluded.

`N` cycles means N full scan-then-sleep rounds, so the run ends by itself; it is not a permanent service. Every cycle appends an `autopilot_cycle` record to `-autopilot-out`, which is what you analyse for opportunity frequency (a cycle with no opportunities writes a record with `opportunities: 0`).

### Reading the result offline

```powershell
go run ./cmd -autopilot-report .\autopilot.jsonl
```

The summary reports the observation window, how many cycles saw an opportunity, opportunities per hour, best/median/mean net edge per $1 basket, the notional that was actually available, filled versus aborted baskets, deployed cash, fees, locked edge, naked-leg exposure, and a per-hour rate. `projected_daily_profit_usd` and `projected_daily_return_pct` are only produced when the observed window is at least 24 hours; shorter runs get `projection_warning` instead, because a 10-minute sample cannot estimate a daily rate. `projected_daily_return_pct` is measured against deployed cash, not your budget, so it overstates the return you would earn on idle capital.

`best_binary_cost_usd`, `best_binary_fee_usd` and `best_binary_net_edge_usd` describe the cheapest YES+NO basket in the scan, even when it was not profitable. They answer "how far from an arbitrage were we?" — a best cost of 1.03 means the market is nowhere near a basket, while 0.998 means the edge is real but too thin to clear your threshold.

### Grouping: which markets deserve attention

The same report groups opportunities so you can compare markets:

- `by_category` — grouped by Gamma `category`; real payloads often leave that empty, so the event's tags are mapped onto a fixed canonical list (Sports, Politics, Elections, Crypto, Economy, Geopolitics, World, Culture, Tech, Business, Science, Health, Climate, Weather) by priority, falling back to the first tag and finally `uncategorized`. Each bucket carries opportunity count, share of all opportunities, distinct events, best/median net edge, available notional, filled baskets and locked edge.
- `by_hour_utc` — cycles grouped by the UTC hour they started in, with opportunities, cycles hit, baskets and locked edge. Hours are UTC, not local time.
- `top_category` — the bucket with the most scanned opportunities, for a quick read.

Categories come from Polymarket metadata: some events expose neither `category` nor tags, so a large `uncategorized` bucket means the grouping is limited by the data, not by the analysis. Compare `opportunities` and `median_net_edge_usd` across buckets, not just counts: a bucket with many tiny edges is worse than one with a few large ones after fees.

`observation_hours` is wall-clock time between the first cycle start and the last cycle finish. If the machine sleeps mid-cycle, that gap is inflated and every per-hour rate is understated; the report then sets `longest_cycle_minutes` and a `data_quality_warnings` entry instead of pretending the run was continuous.

`verdict` is mechanical, never optimistic:

| Verdict | Meaning |
|---|---|
| `NO_DATA` | no completed cycle in the log |
| `NO_OPPORTUNITY` | arbitrage never cleared the edge floor; this strategy has no business in this market |
| `NO_EXECUTION` | opportunities existed but no basket completed (depth, partial fills, cooldown, budget) |
| `NO_EDGE_AFTER_COSTS` | baskets filled but locked no positive edge |
| `POSITIVE_IN_SAMPLE` | positive locked edge in this window only; an in-sample measurement, not a forecast |

The report never turns the projection into a promise: paper fills assume displayed size is executable and locked edge only becomes cash at settlement.

## Research pipeline: collect → samples → calibration

The collector only reads public data and writes JSONL under `data/`. The actual 320-market study and its **NO_EDGE** verdict are in [RESEARCH.md](RESEARCH.md); `data/` and `reports/` contain the matching artifacts.

```powershell
# Collect up to 5 settled binary markets with historical prices (default: last 30 days)
go run ./cmd -collect-resolved 5 -data-dir .\data

# Build timestamped samples at 7d/48h/24h/6h before close, plus reports
go run ./cmd -build-samples -data-dir .\data -report-dir .\reports

# Inspect both baselines
go run ./cmd -calibrate .\reports\samples_market.csv -calibration-bins 10
go run ./cmd -calibrate .\reports\samples_logit.csv -calibration-bins 10
```

`-build-samples` produces:

- `reports/samples_market.csv`: model probability = market price (baseline A)
- `reports/samples_logit.csv`: P(YES) = sigmoid(logit(price) + w × ROC_logit) (transparent baseline B)
- `reports/calibration_*.json`, `reports/sample_summary.json`
- `reports/calibration_*_holdout.json`: last 30% of event groups by resolution time, never used to tune the weight
- `reports/samples_*_holdout.csv`: the held-out event groups only

Both files contain the same rows, so the Brier comparison is on identical decisions. The holdout is split by event group (all markets in a negRisk/multi-market event stay together), not by individual sample. `brier_improvement_logit_vs_market` is positive when the logit baseline has lower Brier. The default logit baseline is deliberately fixed at `w=0.5`; changing `-momentum-weight` after looking at the holdout is overfitting. Continue only if the logit model beats the market price on the chronological holdout, before fees, and then still passes the offline backtest with realistic fees/slippage.

For non-Yes/No binary markets (e.g. Over/Under), `resolved_outcome=YES` means the first outcome, matching the collector's first CLOB token.

CLOB often returns no history for older settled markets. The collector retries at 1-minute fidelity, then skips the market; it never fills missing prices. The default window is 30 days because older history is frequently unavailable. Run the collector periodically to build a real sample.

## Offline backtest

Prepare a CSV with one independent hypothetical trade opportunity per row:

```csv
timestamp,market_id,market_yes_price,model_yes_probability,resolved_outcome
2026-01-01T12:00:00Z,example-market,0.42,0.55,YES
2026-01-02T12:00:00Z,another-market,0.61,0.48,NO
```

Probabilities and prices are decimals from 0 to 1. `timestamp` must be RFC3339. `resolved_outcome` is `YES` or `NO`. The model probability and entry price must have been recorded at the timestamp, before resolution; do not generate predictions using information learned later.

Run from the repository root, and explicitly supply fee and slippage assumptions. The CLI intentionally does not default these costs to zero:

```powershell
go run ./cmd -backtest .\signals.csv -capital 10000 -stake 100 -min-edge 0.05 -fee-bps 200 -slippage-bps 100
```

The tool buys YES when the model probability exceeds the market YES price by at least `min-edge`; it buys NO when the difference is at most the negative threshold. Stake is fixed and trades settle sequentially. Results include trade count, net P&L, return, win rate (percent), profit factor, maximum drawdown (fraction of peak capital), costs, and a per-trade log. Profit factor is `null` when the sample has no losing trades.

`fee-bps` is a simplified fee assumption applied to stake, and `slippage-bps` adversely adjusts the entry price by a percentage of the quoted outcome price. These are configurable approximations, not Polymarket's exact fee schedule or an order-book execution model. The backtest does not model overlapping positions, market impact, partial fills, liquidity limits, funding, or correlated exposure. Use conservative costs and genuinely out-of-sample, timestamped data.

### Keeping credentials out of the repo

`-secrets-audit` walks the working tree, reports committed-looking credentials **without printing their values**, lists which `POLYMARKET_*` variables are present (length only), checks that `polymarket_config.json` is git-ignored, and lists the hosts the client contacts. It is offline.

```powershell
go run ./cmd -secrets-audit
```

The private key and API secret come from the environment only; `polymarket_config.json` should contain addresses and switches, never secrets. If a key was ever pasted into a file or shell history, rotate it on Polymarket — deleting it from the working tree does not remove it from Git history.

## Configuration and current limits

`polymarket_config.example.json` contains an empty template. Copy it to `polymarket_config.json` only if you need to inspect configuration behavior; that local filename is git-ignored.

```powershell
Copy-Item .\polymarket_config.example.json .\polymarket_config.json
go run ./cmd -config .\polymarket_config.json -status
```

Credentials can also come from `POLYMARKET_API_KEY`, `POLYMARKET_API_SECRET`, `POLYMARKET_PASSPHRASE`, `POLYMARKET_WALLET_ADDRESS`, `POLYMARKET_FUNDER_ADDRESS`, `POLYMARKET_SIGNATURE_TYPE`, `POLYMARKET_CHAIN_ID`, `POLYMARKET_PRIVATE_KEY`, and `AI_API_KEY`. The CLOB L2 secret is used only as an HMAC key and is never sent as a header. `POLYMARKET_PRIVATE_KEY` is read from the environment only and is never written to the config file.

The default mode is `paper`, with trading disabled. `-status` reports configuration only; it does not check API connectivity. `-metrics`, `-positions`, `-pause`, and `-resume` still report missing persistent runtime state. The automatic online bot loop is not implemented.

The API packages read Gamma, CLOB order books/prices/history, and Data API positions/trades. The legacy `CLOBService.CreateOrder` still fails closed; real orders go through `OrderSigner` (EIP-712) and `LiveExecutor` (submit + reconcile). A single small order can be signed and sent with the gated CLI below; see [LIVE_TRADING.md](LIVE_TRADING.md) for the full checklist.

### Paper execution

`PaperExecutor` walks the visible ask/bid levels, computes VWAP, supports partial fills and applies a configurable fee. It does not model queue position or latency, so paper results remain optimistic. The strategy engine now uses this executor for paper mode instead of filling instantly at the top price.

### Live order path (off by default)

```powershell
# Read-only pre-flight: config, credentials, wallet model, market, fees,
# book depth and signability. Never posts an order.
go run ./cmd -live-verify <TOKEN_ID> -live-shares 5 -live-max-usd 5

# Dry-run: sign and print the order without sending it
go run ./cmd -live-order <TOKEN_ID> -live-shares 5 -live-price 0.10 -live-max-usd 5

# Actually send a tiny FAK order (multiple guards required)
$env:POLYMARKET_PRIVATE_KEY = '0x...'
$env:POLYMARKET_WALLET_ADDRESS = '0x...'
$env:POLYMARKET_API_KEY = '...'
$env:POLYMARKET_API_SECRET = '...'
$env:POLYMARKET_PASSPHRASE = '...'
go run ./cmd -live-order <TOKEN_ID> -live-shares 5 -live-price 0.10 -live-max-usd 5 `
  -live-dry-run=false -live-confirm=I_UNDERSTAND
```

Sending also requires `trading.mode=live`, `trading.enabled=true`, `trading.allow_live=true`, `POLYMARKET_ALLOW_LIVE=1`, a non-empty `trading.allowed_tokens` whitelist containing the token, an absent STOP file, `-live-confirm=I_UNDERSTAND`, a hard per-order cap of at most $25, and a two-second abort window. Do not use a wallet with funds until you have read [LIVE_TRADING.md](LIVE_TRADING.md) and verified allowances/funder/signature type.

`-live-verify` is the safe first step: it prints one `pass` / `warn` / `fail` / `skip` row per check plus `config_ready`, `market_ready` and `ready_for_small_live_test`. Signing and wallet-model checks are skipped when no private key is set, so it works before you hand over any credential. Chain checks read USDC `allowance`, USDC `balanceOf`, CTF `isApprovedForAll` and the CTF position `balanceOf(owner, tokenId)` over a public Polygon RPC (default `https://polygon-bor-rpc.publicnode.com`; override with `-polygon-rpc` or `POLYMARKET_POLYGON_RPC`). An RPC failure is a warning, not a blocker, and a SELL larger than the held position fails.

The unattended path has the article's safety gates: `STOP` file kill switch, token whitelist, single-order cap, daily-loss/drawdown halts, heartbeat file for `-arb-scan -arb-interval`, and a `-live-cancel` command.

```powershell
# Cancel a live order by ID
go run ./cmd -live-cancel <ORDER_ID>
```

`GetLiveVolume`, `GetTopHolders`, and leaderboard/builder endpoints have not been validated against live responses and should not be relied on. The full list of remaining work is in [REVIEW.md](REVIEW.md).

## Strategy research cautions

The existing analyzer no longer invents a "fair value" from price, volume or order-book imbalance: those heuristics are not calibrated forecasts, and imbalance is often adverse selection rather than an edge. `CalculateFairValue` now returns the market-implied probability unless a caller supplies a validated model probability. `AIPredictor` is an optional LLM layer, not proven alpha. The named strategy implementations have not been validated by a credible backtest. The Kelly sizing code and risk controls are not substitutes for evidence of an edge. No win-rate, Sharpe ratio, or return claim is made by this project. See [REVIEW.md](REVIEW.md) for a realistic discussion of structural arbitrage, market making, event latency, calibration, and why an LLM alone is not an edge.
