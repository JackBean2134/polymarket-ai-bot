package polymarket

import (
	"encoding/json"
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

func TestEvaluateBasketUsesScheduleRateNotTakerBaseFee(t *testing.T) {
	// Regression: the scanner used to charge takerBaseFee (1000) as 10% of
	// notional, which rejected almost every real basket. The protocol fee is
	// rate × p × (1-p): 0.05 × 0.50 × 0.50 + 0.05 × 0.45 × 0.55 = 0.024875.
	event := Event{ID: "e", Title: "sports", Tags: []Tag{{Label: "Sports"}}}
	legs := []ArbitrageLeg{
		{MarketID: "a", Outcome: "YES", Ask: 0.50, AvailableShares: 100, FeeRate: 0.05, FeeExponent: 1, FeeBps: 1000},
		{MarketID: "a", Outcome: "NO", Ask: 0.45, AvailableShares: 100, FeeRate: 0.05, FeeExponent: 1, FeeBps: 1000},
	}
	opp, ok := evaluateBasket(event, "BINARY_YES_NO", legs, 1, 0.005)
	if !ok {
		t.Fatal("a 5 cent basket must survive the real fee schedule")
	}
	if math.Abs(opp.FeeUSD-0.024875) > 1e-9 {
		t.Fatalf("expected 0.024875 fee, got %f", opp.FeeUSD)
	}
	if math.Abs(opp.NetEdgeUSD-0.025125) > 1e-9 {
		t.Fatalf("expected 0.025125 net edge, got %f", opp.NetEdgeUSD)
	}
}

func TestEvaluateBasketRejectsFeeHeavyBasket(t *testing.T) {
	// A 2 cent edge at 54/44 cannot pay a 5% schedule fee (~2.5 cents).
	event := Event{ID: "e", Title: "sports", Tags: []Tag{{Label: "Sports"}}}
	legs := []ArbitrageLeg{
		{MarketID: "a", Outcome: "YES", Ask: 0.54, AvailableShares: 100, FeeRate: 0.05, FeeExponent: 1},
		{MarketID: "a", Outcome: "NO", Ask: 0.44, AvailableShares: 100, FeeRate: 0.05, FeeExponent: 1},
	}
	if _, ok := evaluateBasket(event, "BINARY_YES_NO", legs, 1, 0.005); ok {
		t.Fatal("the fee schedule should reject a 2 cent edge here")
	}
}

func TestEventCategoryResolution(t *testing.T) {
	if got := eventCategory(Event{Category: "Sports"}); got != "Sports" {
		t.Fatalf("expected the Gamma category, got %q", got)
	}
	if got := eventCategory(Event{Tags: []Tag{{Label: "Politics"}}}); got != "Politics" {
		t.Fatalf("expected the first tag label, got %q", got)
	}
	if got := eventCategory(Event{Tags: []Tag{{Name: "Crypto"}}}); got != "Crypto" {
		t.Fatalf("expected the tag name fallback, got %q", got)
	}
	if got := eventCategory(Event{Tags: []Tag{{Slug: "fed"}}}); got != "fed" {
		t.Fatalf("expected the tag slug fallback, got %q", got)
	}
	if got := eventCategory(Event{}); got != "uncategorized" {
		t.Fatalf("expected uncategorized, got %q", got)
	}
	if got := eventCategory(Event{Category: "  ", Tags: []Tag{{Label: "  "}, {Label: "Sports"}}}); got != "Sports" {
		t.Fatalf("expected blank values to be skipped, got %q", got)
	}
	// Real Gamma payloads leave category empty and order tags by specificity.
	if got := eventCategory(Event{Tags: []Tag{{Label: "Esports"}, {Label: "league of legends"}, {Label: "Games"}, {Label: "Sports"}}}); got != "Sports" {
		t.Fatalf("expected the canonical Sports bucket, got %q", got)
	}
	if got := eventCategory(Event{Tags: []Tag{{Label: "Main Election"}, {Label: "Politics"}, {Label: "World"}}}); got != "Politics" {
		t.Fatalf("expected the canonical Politics bucket, got %q", got)
	}
	if got := eventCategory(Event{Tags: []Tag{{Label: "NBA"}, {Slug: "sports"}}}); got != "Sports" {
		t.Fatalf("expected a slug match to map to Sports, got %q", got)
	}
}

func TestEventTagsDecodedFromGammaPayload(t *testing.T) {
	// Shape observed from gamma-api.polymarket.com/events: category is null and
	// tags carry a label field ordered by specificity.
	payload := `{"id":"1262496","slug":"lol-ig1-ly-2026-10-28","title":"LoL",
		"category":null,"negRisk":false,
		"tags":[{"id":"1","label":"Esports","slug":"esports"},
		        {"id":"2","label":"League of Legends","slug":"league-of-legends"},
		        {"id":"3","label":"Sports","slug":"sports"}]}`

	var event Event
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if len(event.Tags) != 3 || event.Tags[0].Label != "Esports" {
		t.Fatalf("tags were not decoded: %+v", event.Tags)
	}
	if got := eventCategory(event); got != "Sports" {
		t.Fatalf("expected Sports bucket, got %q", got)
	}
	if labels := eventTagLabels(event); len(labels) != 3 || labels[2] != "Sports" {
		t.Fatalf("unexpected tag labels: %v", labels)
	}
}

func TestEventTagLabelsDeduplicates(t *testing.T) {
	event := Event{Tags: []Tag{
		{Label: "Sports"},
		{Label: "sports"},
		{Name: "Sports"},
		{Label: "Esports"},
	}}
	labels := eventTagLabels(event)
	if len(labels) != 2 || labels[0] != "Sports" || labels[1] != "Esports" {
		t.Fatalf("unexpected labels: %v", labels)
	}
	if labels := eventTagLabels(Event{}); labels != nil {
		t.Fatalf("expected nil for an event without tags, got %v", labels)
	}
}
