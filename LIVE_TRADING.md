# Live trading checklist (small size only)

> Live ordering is dangerous. The code now signs real CLOB orders, but a successful signature does **not** prove your wallet has the right funder, allowances, API credentials or market access. Start with the dry-run, then a $5 FAK order, and keep the hard cap.

## 1. What is implemented

- EIP-712 order signing for `Polymarket CTF Exchange` (Polygon 137 and Amoy 80002).
- Exact official Order struct, domain name/version, exchange + negRisk exchange addresses, side/signature-type constants.
- Official amount rounding based on the market tick size (`0.1 / 0.01 / 0.001 / 0.0001`).
- Official test vector reproduced byte-for-byte (see `order_signing_test.go`).
- CLOB L2 HMAC headers (`POLY_ADDRESS`, `POLY_SIGNATURE`, `POLY_TIMESTAMP`, `POLY_API_KEY`, `POLY_PASSPHRASE`).
- Tick size, negRisk flag and the legacy `feeRateBps` fetched per token before signing.
- Protocol fee estimation from the market's Gamma `feeSchedule`: `fee = shares × rate × p × (1-p)`. The executor sizes and reports against that number, and refuses to trade when the schedule cannot be resolved instead of guessing from `takerBaseFee`.
- `POST /order`, order polling via `/data/order/{id}`, and cancel via `DELETE /order`.
- Hard per-order USD cap, FAK default, 2-second abort window, no resting GTC left after reconciliation.
- Book-walking paper fills with VWAP and partial fills.

## 2. Pre-flight: `-live-verify`

```powershell
go run ./cmd -live-verify <TOKEN_ID> -live-shares 5 -live-max-usd 5
```

Read-only. Prints one `pass` / `warn` / `fail` / `skip` row for the kill switch, live switches, token whitelist, L2 credentials, signer/POLY_ADDRESS match, wallet model, market tradability, minimum order, tick alignment, `feeSchedule`, the legacy `takerBaseFee` mismatch, book depth and signability, then summarises with `config_ready`, `market_ready` and `ready_for_small_live_test`.

It signs locally to prove the order is buildable but never posts. Allowance checks read USDC `allowance(owner, exchange)`, USDC `balanceOf(owner)`, CTF `isApprovedForAll(owner, exchange)` and the CTF `balanceOf(owner, tokenId)` position through `eth_call` on a public Polygon RPC: default `https://polygon-bor-rpc.publicnode.com`, override with `-polygon-rpc` or `POLYMARKET_POLYGON_RPC`/`POLYGON_RPC_URL`. It selects the negRisk exchange automatically, treats an RPC error as a warning, blocks readiness when the allowance or balance cannot cover the order, and fails a SELL that exceeds the held position.

## 3. What is NOT verified / not implemented

- A real authenticated POST with your credentials. Only the dry-run signature and public endpoints have been exercised in this environment.
- Exact fill VWAP per trade. The live executor uses the limit price (or order price) conservatively; reconcile trades in the UI/API for accounting.
- On-chain order book depth beyond the visible top levels, and per-trade VWAP. Balance and allowance are read, but nothing on-chain is submitted.
- Persistent order/position ledger, stop-loss, automatic cancellation outside the reconciliation window, WebSocket user feeds.
- Multi-order/basket execution. The arbitrage scanner is measurement-only.
- Fee schedules are resolved through Gamma's `clob_token_ids` filter. If that lookup fails (network, or a market with neither `feeSchedule` nor `feesEnabled`), live execution fails closed; it never falls back to treating `takerBaseFee` as a rate.

## 4. Wallet model

Polymarket has three signature types:

| Type | Meaning | maker/funder | signer |
|---|---|---|---|
| `0` | EOA | same as signer | EOA |
| `1` | Polymarket proxy | proxy wallet holding funds | EOA |
| `2` | Polymarket Gnosis safe | safe holding funds | EOA |

Set:

```powershell
$env:POLYMARKET_PRIVATE_KEY = '0x...'          # never commit
$env:POLYMARKET_WALLET_ADDRESS = '0x...'       # EOA signer
$env:POLYMARKET_FUNDER_ADDRESS = '0x...'       # proxy/safe for types 1/2
$env:POLYMARKET_SIGNATURE_TYPE = '0'           # 0, 1, or 2
$env:POLYMARKET_CHAIN_ID = '137'
```

- For type `0`, `FUNDER_ADDRESS` must be empty or equal to the signer.
- For types `1/2`, `FUNDER_ADDRESS` is required.
- If you normally trade through the Polymarket UI, your funds are often in a proxy wallet; do not guess. Check the UI/API and use the correct funder + signature type.
- The EOA needs USDC approval to the appropriate exchange contract. Approvals are an on-chain action; this repository does not submit them.

## 5. API credentials (L2)

Set all of:

```powershell
$env:POLYMARKET_API_KEY = '...'
$env:POLYMARKET_API_SECRET = '...'
$env:POLYMARKET_PASSPHRASE = '...'
$env:POLYMARKET_WALLET_ADDRESS = '0x...'
$env:POLYMARKET_ALLOW_LIVE = '1'       # third live switch
$env:POLYMARKET_ALLOWED_TOKENS = '<TOKEN_ID>,<ANOTHER_TOKEN>'
```

The secret is used only as the HMAC key and is never sent as a header. If the timestamp/signature path/body do not match exactly, the server returns 401.

Config must also allow the live path:

```json
{
  "trading": {
    "mode": "live",
    "enabled": true,
    "allow_live": true,
    "allowed_tokens": ["<TOKEN_ID>"],
    "stop_file": "STOP",
    "heartbeat_file": "heartbeat.txt"
  }
}
```

The triple switch is: `trading.mode=live` + `trading.allow_live=true` + `POLYMARKET_ALLOW_LIVE=1`, with `trading.enabled=true` as the master switch. A non-empty token whitelist is mandatory; an absent STOP file is required for every new order. The automatic bot loop still refuses live mode; only the explicit `-live-order` command uses this path.

## 6. Step 1: dry-run

```powershell
go run ./cmd -live-order <TOKEN_ID> `
  -live-shares 5 `
  -live-price 0.10 `
  -live-max-usd 5
```

This fetches tick size, negRisk and fee rate, builds the EIP-712 order, and prints:

- maker/signer/funder
- tokenId
- makerAmount / takerAmount in 1e6 units
- feeRateBps
- salt, signature, order digest
- tick size and negRisk

Compare `maker`/`signer`/`taker`/amounts with what you expect. A 5-share BUY at 0.10 should be `makerAmount=500000`, `takerAmount=5000000`, i.e. $0.50 for 5 shares.

## 7. Step 2: send a tiny order

```powershell
go run ./cmd -live-order <TOKEN_ID> `
  -live-shares 5 `
  -live-price 0.10 `
  -live-max-usd 5 `
  -live-order-type FAK `
  -live-dry-run=false `
  -live-confirm=I_UNDERSTAND
```

Guards:

- `-live-confirm=I_UNDERSTAND`
- `trading.mode=live` + `trading.enabled=true` + `trading.allow_live=true`
- `POLYMARKET_ALLOW_LIVE=1`
- token must be in non-empty `trading.allowed_tokens`
- STOP file must not exist
- all L2 credentials + private key
- `-live-max-usd` <= $25 and <= `risk.max_position_size`
- notional <= max USD; fees are budgeted in the executor
- 2-second abort window (Ctrl+C)
- FAK by default; if a GTC order is still live after 8 seconds it is cancelled
- if the exchange reports a match but no matched size, the executor refuses to invent a fill

## 8. Cancellation

```powershell
go run ./cmd -live-cancel <ORDER_ID>
```

This uses the L2 credentials and `DELETE /order`. The live executor also cancels a resting GTC order automatically after its 8-second reconciliation window. Always check the UI/order endpoint to confirm the order is gone.

## 9. Common failures

| Symptom | Likely cause |
|---|---|
| 401 | API secret/passphrase wrong, timestamp skew, wrong POLY_ADDRESS, body/path mismatch |
| 400 `invalid signature` | Wrong signature type, funder, chain, exchange address, or token ID |
| 400 tick size / price | Price not aligned to tick, or outside `[tick, 1-tick]` |
| 400 fee rate | `feeRateBps` does not match the market's `base_fee` |
| order rejected / insufficient balance | Missing USDC or CTF allowance, or wrong funder |
| fill unknown | Exchange response/reconciliation did not expose matched size; do not assume a fill |

## 10. Do not

- Do not commit the private key or API secret.
- Do not use a wallet with meaningful funds for the first test.
- Do not disable the hard cap without changing code deliberately.
- Do not run with an empty token whitelist or a present STOP file.
- Do not treat the arbitrage scanner output as executable liquidity; books move.
- Do not leave GTC orders resting while you debug; cancel them in the UI.

## 11. Kill switch and heartbeat

Create a file named `STOP` (or whatever `trading.stop_file` points to) to make the next live order and the next `-arb-scan` round fail closed. Delete it only after you have inspected the state.

`-arb-scan -arb-interval` writes `trading.heartbeat_file` after each successful scan. A watchdog should alert if the file is older than 15 minutes.

## 12. First $5 order, step by step

**Step 1 — read-only pre-flight** (no key needed for the public checks):

```powershell
$env:POLYMARKET_WALLET_ADDRESS = '0x...'          # signer EOA
$env:POLYMARKET_FUNDER_ADDRESS = '0x...'          # only for signature types 1/2
go run ./cmd -live-verify <TOKEN_ID> -live-shares 5 -live-max-usd 5
```

Fix every `fail` before continuing (`credentials.*` and signing checks may still be `skip` without a key).

**Step 2 — create `polymarket_config.json`** (gitignored; never commit it):

```json
{
  "polymarket": {
    "wallet_address": "0x...",
    "funder_address": "0x...",
    "signature_type": 1,
    "chain_id": 137
  },
  "trading": {
    "enabled": true,
    "mode": "live",
    "allow_live": true,
    "allowed_tokens": ["<TOKEN_ID>"],
    "stop_file": "STOP",
    "heartbeat_file": "heartbeat.txt"
  }
}
```

**Step 3 — credentials come from the environment only**:

```powershell
$env:POLYMARKET_API_KEY = '...'
$env:POLYMARKET_API_SECRET = '...'
$env:POLYMARKET_PASSPHRASE = '...'
$env:POLYMARKET_PRIVATE_KEY = '0x...'
$env:POLYMARKET_ALLOW_LIVE = '1'
```

**Step 4 — re-run the pre-flight** until `config_ready` and `market_ready` are both `true` and `ready_for_small_live_test` is `true`.

**Step 5 — sign without sending**:

```powershell
go run ./cmd -live-order <TOKEN_ID> -live-shares 5 -live-price 0.10 -live-max-usd 5
```

Check the preview: `signer`/`funder` match your wallet model, `fee_rate_bps` matches the market, `taker_fee.rate` matches the `feeSchedule`, and an `order_hash` is printed.

**Step 6 — send exactly one $5 FAK order**:

```powershell
go run ./cmd -live-order <TOKEN_ID> -live-shares 5 -live-price 0.10 -live-max-usd 5 `
  -live-dry-run=false -live-confirm=I_UNDERSTAND
```

The CLI prints the preview, waits 2 seconds (Ctrl+C aborts), posts, polls `size_matched` for up to 8 seconds and cancels any remainder.

**Step 7 — cancel by id if anything looks wrong**:

```powershell
go run ./cmd -live-cancel <ORDER_ID>
```

**Step 8 — reconcile before sizing up**: compare the fill with `-wallet 0x...` positions and with `-live-verify` balances. If the fill or fee differs from the estimate, stop and investigate; do not send a second order to "fix" it.

Create a `STOP` file at any point to block the next order.

## 13. RPC endpoints for allowance and balance checks

`-live-verify` reads chain state with `eth_call`. Keyless public endpoints work but are rate limited:

| Endpoint | Status |
|---|---|
| `https://polygon-bor-rpc.publicnode.com` | default |
| `https://polygon.drpc.org` | keyless |
| `https://1rpc.io/matic` | keyless |
| `https://polygon-rpc.com` | returns 401 (needs a key) |

For regular use, create a free endpoint (Alchemy, Infura, QuickNode, Ankr) and point the tool at it:

```powershell
$env:POLYMARKET_POLYGON_RPC = 'https://polygon-mainnet.g.alchemy.com/v2/<KEY>'
go run ./cmd -live-verify <TOKEN_ID> -live-shares 5
```

or pass `-polygon-rpc <URL>` (`POLYGON_RPC_URL` is also honoured as a fallback).

Rules:

- The key is part of the URL, so treat it as a secret: keep it in the environment, never in the repo.
- One `-live-verify` run makes four calls: USDC allowance, USDC balance, CTF approval, CTF position.
- An RPC failure becomes a `warn`, never a fake `pass`.
- Only Polygon mainnet (137) is supported; other chains report `skip`.

