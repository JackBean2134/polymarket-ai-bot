# Connection and execution status

The CLI supports read-only connections to Gamma, CLOB and Data API (`-markets`, `-market`, `-wallet`, `-arb-scan`), book-walking paper fills, and a gated single-order live path. The automatic online trading loop, persistent ledger, and WebSocket reconciliation are not implemented. The CLI still refuses live mode for the automatic bot; live orders use the explicit `-live-order` command with `trading.mode=live`, `trading.enabled=true`, and `-live-confirm=I_UNDERSTAND`.

No API credentials are required for `go test ./...`, the offline CSV backtest, or the read-only commands. The `-status` command only reports loaded configuration and does not test connectivity or private authentication. See [README.md](README.md) for commands, [LIVE_TRADING.md](LIVE_TRADING.md) for the live checklist, and [REVIEW.md](REVIEW.md) for the review and profitability analysis.

Paper fills now walk the visible order book, but they still do not model queue position or latency. Live order signing uses EIP-712 with the official test vector; a real authenticated POST/cancel with your credentials still needs to be verified before meaningful size.

Order submission is gated; the legacy `CreateOrder` method fails closed and real orders go through `OrderSigner` + `LiveExecutor`. Any credentials previously committed to this repository should be revoked and rotated; removing a secret from the current checkout does not remove it from Git history.
