# Quickstart

This is an experimental research codebase. The CLI supports offline backtests and read-only market research. It does not run a live bot; order submission is deliberately disabled. Do not configure real trading credentials or enable live mode.

## Verify the code

From the repository root:

```powershell
go test ./...
go run ./cmd -h
```

## Read-only market research

```powershell
go run ./cmd -markets 20
go run ./cmd -market 559651
go run ./cmd -wallet 0x91430CaD2d3975766499717fA0D66A78D814E5c5
go run ./cmd -arb-scan 5 -arb-min-edge 0.005 -arb-out .\opportunities.jsonl
```

All prices are decimals in `[0, 1]`. These commands only read public data and never sign or submit orders.

## Sign a live order without sending it

```powershell
$env:POLYMARKET_PRIVATE_KEY = '0x...'
$env:POLYMARKET_WALLET_ADDRESS = '0x...'
go run ./cmd -live-order <TOKEN_ID> -live-shares 5 -live-price 0.10 -live-max-usd 5
```

This is a dry-run by default. Read [LIVE_TRADING.md](LIVE_TRADING.md) before adding `-live-dry-run=false -live-confirm=I_UNDERSTAND`.

## Run a local backtest

Create `signals.csv` with predictions captured before resolution:

```csv
timestamp,market_id,market_yes_price,model_yes_probability,resolved_outcome
2026-01-01T12:00:00Z,example-market,0.42,0.55,YES
2026-01-02T12:00:00Z,another-market,0.61,0.48,NO
```

Then run:

```powershell
go run ./cmd -backtest .\signals.csv -capital 10000 -stake 100 -min-edge 0.05 -fee-bps 200 -slippage-bps 100
```

All probabilities and prices are decimals from 0 to 1. Fee and slippage are simplified configurable estimates, not the exchange's exact fee schedule or a fill simulator. Review the assumptions and use timestamped out-of-sample data; a backtest cannot guarantee future performance.

See [README.md](README.md) for CSV semantics, limitations, and the current implementation boundary.
