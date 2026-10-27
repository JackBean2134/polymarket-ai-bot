# Current status

- The project is experimental and has no demonstrated positive returns.
- The CLI supports offline CSV backtesting and read-only research:
  - `-markets N` lists active binary markets by volume/liquidity;
  - `-market <gamma-id>` shows Gamma metadata, YES token IDs, order book and midpoint;
  - `-wallet <address>` shows Data API positions and recent trades;
  - `-arb-scan N` scans YES/NO and negRisk baskets and can append JSONL (`-arb-out`, `-arb-interval`).
- Gamma/CLOB/Data parsing matches live payloads (arrays, string-encoded arrays/numbers, `token_id`, decimal prices).
- `-collect-resolved N` writes settled binary markets and historical price points to JSONL; `-build-samples` turns them into timestamped CSVs and calibration reports for a market-price baseline and a logit-momentum baseline.
- Latest real study: 320 settled markets, 309 with usable samples, 883 decisions, 255 event clusters. Event-group holdout decision is **NO_EDGE** (Brier 0.241158 vs market 0.241153 full; holdout bootstrap interval crosses zero). See [RESEARCH.md](RESEARCH.md).
- `-market` now also prints logit-space price features (ROC_logit, ATR_logit, EMA, Aroon) plus order-book depth/spread and the article's momentum filter decision.
- `-calibrate <csv>` reports Brier score, log loss and reliability buckets.
- Paper-mode strategy execution now walks the visible order book (VWAP, partial fills) instead of filling at the top price.
- Decision logs can record signals, fills and skips as JSONL; scan results include `skip_reasons` counts.
- EIP-712 order signing is implemented and verified against the official `python-order-utils` test vector. `LiveExecutor` submits and reconciles a single small order.
- Live ordering remains off by default. It requires `trading.mode=live`, `trading.enabled=true`, `trading.allow_live=true`, `POLYMARKET_ALLOW_LIVE=1`, a non-empty `trading.allowed_tokens` whitelist containing the token, an absent STOP file, all L2 credentials, a private key from the environment, `-live-dry-run=false`, `-live-confirm=I_UNDERSTAND`, and a hard cap of at most $25 per order.
- A real authenticated POST/cancel with your credentials has not been verified in this environment; exact per-trade VWAP, persistent ledger, on-chain allowance checks and WebSocket reconciliation are not implemented.
- The previous version of this document contained exposed API credentials. Those values were removed from the current revision, but remain in Git history unless the repository owner separately removes them. Revoke and rotate them immediately if valid.

Run `go test ./...` and `go vet ./...` from the repository root. See [README.md](README.md) for commands, [LIVE_TRADING.md](LIVE_TRADING.md) for the live-order checklist, and [REVIEW.md](REVIEW.md) for the full review and profitability analysis.

