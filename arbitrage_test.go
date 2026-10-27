package polymarket

import (
	"math"
	"testing"
)

func TestEvaluateBasketYesArbitrage(t *testing.T) {
	event := Event{ID: "e", Slug: "slug", Title: "title"}
	legs := []ArbitrageLeg{
		{MarketID: "a", Outcome: "YES", Ask: 0.40, AvailableShares: 100, FeeBps: 0},
		{MarketID: "b", Outcome: "YES", Ask: 0.50, AvailableShares: 50, FeeBps: 0},
	}
	opp, ok := evaluateBasket(event, "NEGRISK_YES", legs, 1, 0.005)
	if !ok {
		t.Fatal("expected an arbitrage opportunity")
	}
	if math.Abs(opp.CostUSD-0.90) > 1e-9 || math.Abs(opp.NetEdgeUSD-0.10) > 1e-9 {
		t.Fatalf("unexpected cost/edge: %+v", opp)
	}
	if opp.MaxBaskets != 50 || math.Abs(opp.NetProfitUSD-5) > 1e-9 {
		t.Fatalf("unexpected size/profit: %+v", opp)
	}
}

func TestEvaluateBasketNoArbitrageWithFees(t *testing.T) {
	event := Event{ID: "e", Title: "title"}
	legs := []ArbitrageLeg{
		{MarketID: "a", Outcome: "NO", Ask: 0.90, AvailableShares: 100, FeeBps: 100},
		{MarketID: "b", Outcome: "NO", Ask: 0.95, AvailableShares: 100, FeeBps: 100},
	}
	opp, ok := evaluateBasket(event, "NEGRISK_NO", legs, 2, 0.001)
	if !ok {
		t.Fatal("expected a NO basket opportunity")
	}
	// cost 1.85, fee 0.0185, payout 2 => net 0.1315
	if math.Abs(opp.FeeUSD-0.0185) > 1e-9 || math.Abs(opp.NetEdgeUSD-0.1315) > 1e-9 {
		t.Fatalf("unexpected fee/net edge: %+v", opp)
	}
}

func TestEvaluateBasketRejectsThinOrNegativeEdges(t *testing.T) {
	event := Event{ID: "e"}
	legs := []ArbitrageLeg{
		{MarketID: "a", Ask: 0.50, AvailableShares: 100, FeeBps: 1000},
		{MarketID: "b", Ask: 0.499, AvailableShares: 100, FeeBps: 1000},
	}
	if _, ok := evaluateBasket(event, "NEGRISK_YES", legs, 1, 0.001); ok {
		t.Fatal("fees should eliminate this edge")
	}
}

func TestBestAskFindsMinimum(t *testing.T) {
	book := &OrderBook{Asks: []Order{
		{Price: 0.40, Size: 10},
		{Price: 0.35, Size: 20},
	}}
	price, size, ok := bestAsk(book)
	if !ok || price != 0.35 || size != 20 {
		t.Fatalf("unexpected best ask: %f %f %v", price, size, ok)
	}
}
