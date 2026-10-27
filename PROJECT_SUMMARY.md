# Project status

This repository is experimental research code, not a production trading bot and not evidence of profitability.

## What currently works

- Go unit tests, `go vet`, and live-payload parsing fixtures.
- Offline CSV backtests using timestamped model probabilities, entry prices, and resolved outcomes.
- Read-only Gamma/CLOB market snapshots and Data API wallet inspection (`-markets`, `-market`, `-wallet`).
- Read-only YES/NO and negRisk basket arbitrage scanning with optional JSONL logging and skip-reason counts.
- Read-only collector for settled markets + price history, timestamped sample builder, and market-price vs logit-momentum baseline calibration reports.
- Actual 320-market study is included under `data/` and `reports/`; its event-group holdout verdict is **NO_EDGE**. See [RESEARCH.md](RESEARCH.md).
- Logit-space features (ROC/ATR/EMA/Aroon), order-book features, and the article's momentum entry filter.
- Calibration report: Brier score, log loss and reliability buckets (`-calibrate`).
- Decision JSONL logger for signals, fills and skips.
- Book-walking paper fills with VWAP/partial fills, risk-approved USD sizing, and settlement/capital release.
- EIP-712 single-order signing (official test vector verified) and a gated live executor with a $25 hard cap, token whitelist, STOP kill switch and heartbeat.
- Config validation and a CLI that refuses the automatic bot in live mode.

## Not implemented

- A persistent online scanning/trading service and WebSocket user/order reconciliation.
- Persistent trade history, positions, or runtime metrics across restarts.
- Exact per-trade VWAP reconciliation, on-chain allowance/balance checks, and multi-leg basket execution.
- A validated, calibrated forecasting edge.

See [README.md](README.md) for commands, [LIVE_TRADING.md](LIVE_TRADING.md) for the live checklist, and [REVIEW.md](REVIEW.md) for the detailed review and profitability analysis. Any API credentials previously committed to Git should be rotated; removing a file from the current revision does not erase historical commits.
