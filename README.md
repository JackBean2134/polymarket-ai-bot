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

Prices are printed as decimals in `[0, 1]` (Polymarket quotes `0.58`, not `58` cents). `-markets` defaults to excluding near-resolved markets; add `-min-price`, `-max-price`, and `-min-days` for your own universe. The arbitrage scanner uses best asks, visible size and Gamma `takerBaseFee`; it is a measurement tool, not an execution guarantee.

### Arbitrage definitions

- `BINARY_YES_NO`: buy YES and NO in the same market; payout is $1 per basket. Opportunity when `yesAsk + noAsk + fees < 1`.
- `NEGRISK_YES`: in a negRisk event with N mutually exclusive markets, buy YES on all N; exactly one pays $1. Opportunity when `sum(yesAsk) + fees < 1`.
- `NEGRISK_NO`: buy NO on all N; N-1 pay $1. Opportunity when `sum(noAsk) + fees < N-1`.

The scanner requires every market in the event to be open and fetchable before reporting a basket. It reports `max_baskets` from top-of-book size and per-basket net edge after the configured fee estimate. Use JSONL output to measure how often and how long opportunities survive before building execution.

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

The unattended path has the article's safety gates: `STOP` file kill switch, token whitelist, single-order cap, daily-loss/drawdown halts, heartbeat file for `-arb-scan -arb-interval`, and a `-live-cancel` command.

```powershell
# Cancel a live order by ID
go run ./cmd -live-cancel <ORDER_ID>
```

`GetLiveVolume`, `GetTopHolders`, and leaderboard/builder endpoints have not been validated against live responses and should not be relied on. The full list of remaining work is in [REVIEW.md](REVIEW.md).

## Strategy research cautions

The existing analyzer no longer invents a "fair value" from price, volume or order-book imbalance: those heuristics are not calibrated forecasts, and imbalance is often adverse selection rather than an edge. `CalculateFairValue` now returns the market-implied probability unless a caller supplies a validated model probability. `AIPredictor` is an optional LLM layer, not proven alpha. The named strategy implementations have not been validated by a credible backtest. The Kelly sizing code and risk controls are not substitutes for evidence of an edge. No win-rate, Sharpe ratio, or return claim is made by this project. See [REVIEW.md](REVIEW.md) for a realistic discussion of structural arbitrage, market making, event latency, calibration, and why an LLM alone is not an edge.
