# Research assessment

This report is generated from settled markets and pre-close prices. It is research, not a profitability guarantee.

- Markets in data: 320
- Markets with samples: 309
- Sample decisions: 883

## Full sample

| Model | Brier | Log loss | Samples |
|---|---:|---:|---:|
| Market price | 0.241153 | 0.672316 | 883 |
| Logit momentum | 0.241158 | 0.675816 | 883 |

## Event-group holdout

| Model | Brier | Log loss | Samples |
|---|---:|---:|---:|
| Market price | 0.232924 | 0.626911 | 335 |
| Logit momentum | 0.232612 | 0.626431 | 335 |

## Decision

**NO_EDGE**

- model does not beat the market baseline on the full sample
- model does not beat the market baseline on the event-group holdout
- holdout bootstrap 95% interval includes zero

## Event-cluster bootstrap

Positive improvement means the model has lower Brier than the market price.

| Window | Markets | Event clusters | Mean Brier improvement | 95% interval |
|---|---:|---:|---:|---:|
| Full | 309 | 255 | -0.000419 | [-0.001188, 0.000344] |
| Holdout | 110 | 77 | -0.000309 | [-0.001873, 0.001203] |

## Required next checks

- Keep trading disabled until the holdout decision is PROMISING with enough markets.
- Run the offline backtest with realistic fees and slippage.
- Paper trade for at least two weeks and verify fills, fees and settlement before risking capital.
