package polymarket

import (
	"encoding/json"
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9
}

func TestTakerFeeMatchesOfficialTable(t *testing.T) {
	// docs: 100 shares, Sports (rate 0.05), fee peaks at 50¢ ($1.25).
	cases := []struct {
		price float64
		rate  float64
		want  float64
	}{
		{0.50, 0.05, 1.25},
		{0.10, 0.05, 0.45},
		{0.25, 0.05, 0.9375},
		{0.75, 0.05, 0.9375},
		{0.90, 0.05, 0.45},
		{0.50, 0.07, 1.75}, // Crypto peaks at $1.75
		{0.50, 0.04, 1.00},
	}
	for _, tc := range cases {
		got := TakerFeeUSD(tc.price, 100, tc.rate, 1)
		if math.Abs(got-tc.want) > 1e-6 {
			t.Fatalf("price %.2f rate %.2f: expected %.4f, got %.4f", tc.price, tc.rate, tc.want, got)
		}
	}
}

func TestTakerFeeIsSymmetricAndGuarded(t *testing.T) {
	if !almostEqual(TakerFeeUSD(0.3, 100, 0.05, 1), TakerFeeUSD(0.7, 100, 0.05, 1)) {
		t.Fatal("fee must be symmetric around 50 cents")
	}
	if TakerFeeUSD(0, 100, 0.05, 1) != 0 || TakerFeeUSD(1, 100, 0.05, 1) != 0 {
		t.Fatal("out-of-range prices must not be charged")
	}
	if TakerFeeUSD(0.5, 0, 0.05, 1) != 0 || TakerFeeUSD(0.5, 100, 0, 1) != 0 {
		t.Fatal("zero shares or zero rate must not be charged")
	}
	// exponent 2 narrows the curve: 100 × 0.05 × 0.5² × 0.5² = 0.3125
	if got := TakerFeeUSD(0.5, 100, 0.05, 2); math.Abs(got-0.3125) > 1e-9 {
		t.Fatalf("unexpected exponent=2 fee: %f", got)
	}
	// A missing exponent must behave like exponent 1.
	if !almostEqual(TakerFeeUSD(0.4, 100, 0.05, 0), TakerFeeUSD(0.4, 100, 0.05, 1)) {
		t.Fatal("exponent <= 0 must default to 1")
	}
}

func TestDefaultTakerFeeRateTable(t *testing.T) {
	cases := map[string]float64{
		"Sports":      0.05,
		"sports":      0.05,
		"Crypto":      0.07,
		"Politics":    0.04,
		"Tech":        0.04,
		"Geopolitics": 0,
		"World":       0,
		"Unknown":     defaultTakerFeeRate,
		"":            defaultTakerFeeRate,
	}
	for category, want := range cases {
		if got := DefaultTakerFeeRate(category); !almostEqual(got, want) {
			t.Fatalf("category %q: expected %.2f, got %.2f", category, want, got)
		}
	}
}

func TestMarketFeeRatePrefersSchedule(t *testing.T) {
	market := Market{FeeRate: 0.03}
	if got := MarketFeeRate(market, "Sports"); !almostEqual(got, 0.03) {
		t.Fatalf("schedule rate must win, got %f", got)
	}
	if got := MarketFeeRate(Market{}, "Sports"); !almostEqual(got, 0.05) {
		t.Fatalf("expected the Sports default, got %f", got)
	}
	if got := MarketFeeRate(Market{}, "Geopolitics"); got != 0 {
		t.Fatalf("geopolitics must be fee-free, got %f", got)
	}
	if got := MarketFeeExponent(Market{}); got != 1 {
		t.Fatalf("default exponent must be 1, got %f", got)
	}
	// An explicit zero_fees schedule must not fall back to the category rate.
	zero := Market{FeeRateKnown: true, FeeRate: 0}
	if got := MarketFeeRate(zero, "Politics"); got != 0 {
		t.Fatalf("explicit zero schedule must stay free, got %f", got)
	}
}

func TestMarketDecodesFeeScheduleInsteadOfTakerBaseFee(t *testing.T) {
	payload := `{
		"id":"6280196",
		"question":"LoL: Invictus Gaming vs LYON - Game 1 Winner",
		"slug":"lol-ig1-ly-2026-10-28-game1",
		"outcomes":"[\"Yes\",\"No\"]",
		"outcomePrices":"[\"0.5\",\"0.5\"]",
		"clobTokenIds":"[\"111\",\"222\"]",
		"takerBaseFee":1000,
		"makerBaseFee":1000,
		"feesEnabled":true,
		"feeType":"sports_fees_v3",
		"feeSchedule":{"exponent":1,"rate":0.05,"takerOnly":true,"rebateRate":0.15}
	}`

	var market Market
	if err := json.Unmarshal([]byte(payload), &market); err != nil {
		t.Fatalf("decode market: %v", err)
	}
	if !almostEqual(market.FeeRate, 0.05) {
		t.Fatalf("expected feeSchedule rate 0.05, got %f", market.FeeRate)
	}
	if market.FeeExponent != 1 || !market.FeesEnabled {
		t.Fatalf("unexpected schedule metadata: %+v", market)
	}
	if !market.FeeRateKnown {
		t.Fatal("a present feeSchedule must mark the rate as known")
	}
	if market.TakerBaseFee != 1000 {
		t.Fatalf("legacy takerBaseFee should still be exposed, got %d", market.TakerBaseFee)
	}
	// The legacy 1000 bps value must never be treated as a 10% rate.
	rate := MarketFeeRate(market, "Sports")
	if rate > 0.1 {
		t.Fatalf("fee rate is implausible: %f", rate)
	}
}

func TestMarketDecodesZeroFeeSchedule(t *testing.T) {
	payload := `{
		"id":"682705",
		"question":"Which party will win the House in 2026?",
		"outcomes":"[\"Yes\",\"No\"]",
		"outcomePrices":"[\"0.5\",\"0.5\"]",
		"clobTokenIds":"[\"111\",\"222\"]",
		"feesEnabled":true,
		"feeType":"zero_fees",
		"feeSchedule":{"exponent":1,"rate":0,"takerOnly":true,"rebateRate":0}
	}`

	var market Market
	if err := json.Unmarshal([]byte(payload), &market); err != nil {
		t.Fatalf("decode market: %v", err)
	}
	if !market.FeeRateKnown || market.FeeRate != 0 {
		t.Fatalf("expected an explicit zero rate, got %+v", market)
	}
	if got := MarketFeeRate(market, "Politics"); got != 0 {
		t.Fatalf("zero_fees market must stay free, got %f", got)
	}
}

func TestFeeModelFallsBackToBps(t *testing.T) {
	model := FeeModel{Bps: 100}
	if got := model.FeeForLevel(0.5, 100); !almostEqual(got, 0.5) {
		t.Fatalf("expected the legacy bps fee, got %f", got)
	}
	official := FeeModel{Rate: 0.05, Exponent: 1}
	if got := official.FeeForLevel(0.5, 100); !almostEqual(got, 1.25) {
		t.Fatalf("expected the official fee, got %f", got)
	}
	if got := (FeeModel{}).FeeForLevel(0.5, 100); got != 0 {
		t.Fatalf("expected no fee, got %f", got)
	}
}
