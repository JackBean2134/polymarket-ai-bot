package polymarket

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestBacktestCSVAccountsForFeesAndSlippage(t *testing.T) {
	input := `timestamp,market_id,market_yes_price,model_yes_probability,resolved_outcome
2026-01-02T00:00:00Z,market-b,0.40,0.60,YES
2026-01-01T00:00:00Z,market-a,0.60,0.40,NO
2026-01-03T00:00:00Z,market-c,0.50,0.52,YES
`
	result, err := BacktestCSV(strings.NewReader(input), BacktestOptions{
		StartingCapital: 1000,
		StakeUSD:        100,
		MinEdge:         0.05,
		FeeBps:          100,
		SlippageBps:     100,
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Signals != 3 || result.Trades != 2 || result.SkippedForEdge != 1 {
		t.Fatalf("unexpected signal/trade counts: %+v", result)
	}
	if result.TradeLog[0].MarketID != "market-a" || result.TradeLog[1].MarketID != "market-b" {
		t.Fatalf("trades are not ordered by timestamp: %+v", result.TradeLog)
	}
	if result.TradeLog[0].Outcome != "NO" || result.TradeLog[0].FeeUSD != 1 {
		t.Fatalf("unexpected first trade: %+v", result.TradeLog[0])
	}
	if result.TradeLog[0].EntryPrice <= 0.4 {
		t.Fatalf("slippage was not applied to entry price: %f", result.TradeLog[0].EntryPrice)
	}
	if result.NetPnL <= 0 || result.FeesUSD != 2 || math.Abs(result.FinalCapital-(1000+result.NetPnL)) > 1e-9 {
		t.Fatalf("net results do not include expected costs: %+v", result)
	}
	if result.WinRate != 100 || result.ProfitFactor != nil {
		t.Fatalf("expected 100 percent win rate and undefined profit factor: %+v", result)
	}
	if result.TradeLog[0].Timestamp.After(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected trade timestamp: %v", result.TradeLog[0].Timestamp)
	}
}

func TestBacktestCSVRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		csv  string
		opts BacktestOptions
	}{
		{
			name: "missing column",
			csv:  "timestamp,market_id\n",
			opts: BacktestOptions{StartingCapital: 1000, StakeUSD: 100, MinEdge: 0.05},
		},
		{
			name: "invalid probability",
			csv:  "timestamp,market_id,market_yes_price,model_yes_probability,resolved_outcome\n2026-01-01T00:00:00Z,m,1.2,0.5,YES\n",
			opts: BacktestOptions{StartingCapital: 1000, StakeUSD: 100, MinEdge: 0.05},
		},
		{
			name: "invalid option",
			csv:  "timestamp,market_id,market_yes_price,model_yes_probability,resolved_outcome\n2026-01-01T00:00:00Z,m,0.5,0.7,YES\n",
			opts: BacktestOptions{StartingCapital: 1000, StakeUSD: 100, MinEdge: 0},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BacktestCSV(strings.NewReader(test.csv), test.opts); err == nil {
				t.Fatal("expected invalid input to fail")
			}
		})
	}
}

func TestBacktestCSVTracksLosingTradesAndDrawdown(t *testing.T) {
	input := `timestamp,market_id,market_yes_price,model_yes_probability,resolved_outcome
2026-01-01T00:00:00Z,market-a,0.50,0.70,NO
`
	result, err := BacktestCSV(strings.NewReader(input), BacktestOptions{
		StartingCapital: 1000,
		StakeUSD:        100,
		MinEdge:         0.05,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NetPnL != -100 || result.MaxDrawdown != 0.1 || result.FinalCapital != 900 {
		t.Fatalf("unexpected loss metrics: %+v", result)
	}
}
