# Live trading checklist (small size only)

> Live ordering is dangerous. The code now signs real CLOB orders, but a successful signature does **not** prove your wallet has the right funder, allowances, API credentials or market access. Start with the dry-run, then a $5 FAK order, and keep the hard cap.

## 1. What is implemented

- EIP-712 order signing for `Polymarket CTF Exchange` (Polygon 137 and Amoy 80002).
- Exact official Order struct, domain name/version, exchange + negRisk exchange addresses, side/signature-type constants.
- Official amount rounding based on the market tick size (`0.1 / 0.01 / 0.001 / 0.0001`).
- Official test vector reproduced byte-for-byte (see `order_signing_test.go`).
- CLOB L2 HMAC headers (`POLY_ADDRESS`, `POLY_SIGNATURE`, `POLY_TIMESTAMP`, `POLY_API_KEY`, `POLY_PASSPHRASE`).
- Tick size, negRisk flag and fee rate fetched per token before signing.
- `POST /order`, order polling via `/data/order/{id}`, and cancel via `DELETE /order`.
- Hard per-order USD cap, FAK default, 2-second abort window, no resting GTC left after reconciliation.
- Book-walking paper fills with VWAP and partial fills.

## 2. What is NOT verified / not implemented

- A real authenticated POST with your credentials. Only the dry-run signature and public endpoints have been exercised in this environment.
- Exact fill VWAP per trade. The live executor uses the limit price (or order price) conservatively; reconcile trades in the UI/API for accounting.
- On-chain balance/allowance checks. The exchange will reject the order if USDC/CTF approvals are missing.
- Persistent order/position ledger, stop-loss, automatic cancellation outside the reconciliation window, WebSocket user feeds.
- Multi-order/basket execution. The arbitrage scanner is measurement-only.

## 3. Wallet model

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

## 4. API credentials (L2)

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

## 5. Step 1: dry-run

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

## 6. Step 2: send a tiny order

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

## 7. Cancellation

```powershell
go run ./cmd -live-cancel <ORDER_ID>
```

This uses the L2 credentials and `DELETE /order`. The live executor also cancels a resting GTC order automatically after its 8-second reconciliation window. Always check the UI/order endpoint to confirm the order is gone.

## 8. Common failures

| Symptom | Likely cause |
|---|---|
| 401 | API secret/passphrase wrong, timestamp skew, wrong POLY_ADDRESS, body/path mismatch |
| 400 `invalid signature` | Wrong signature type, funder, chain, exchange address, or token ID |
| 400 tick size / price | Price not aligned to tick, or outside `[tick, 1-tick]` |
| 400 fee rate | `feeRateBps` does not match the market's `base_fee` |
| order rejected / insufficient balance | Missing USDC or CTF allowance, or wrong funder |
| fill unknown | Exchange response/reconciliation did not expose matched size; do not assume a fill |

## 9. Do not

- Do not commit the private key or API secret.
- Do not use a wallet with meaningful funds for the first test.
- Do not disable the hard cap without changing code deliberately.
- Do not run with an empty token whitelist or a present STOP file.
- Do not treat the arbitrage scanner output as executable liquidity; books move.
- Do not leave GTC orders resting while you debug; cancel them in the UI.

## 10. Kill switch and heartbeat

Create a file named `STOP` (or whatever `trading.stop_file` points to) to make the next live order and the next `-arb-scan` round fail closed. Delete it only after you have inspected the state.

`-arb-scan -arb-interval` writes `trading.heartbeat_file` after each successful scan. A watchdog should alert if the file is older than 15 minutes.
