package polymarket

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestEvaluateCalibration(t *testing.T) {
	samples := []CalibrationSample{
		{Predicted: 0.7, Outcome: 1},
		{Predicted: 0.2, Outcome: 0},
		{Predicted: 0.6, Outcome: 1},
		{Predicted: 0.4, Outcome: 0},
	}
	report := EvaluateCalibration(samples, 10)
	if report.Samples != 4 {
		t.Fatalf("unexpected sample count: %+v", report)
	}
	if math.Abs(report.BrierScore-0.1125) > 1e-9 {
		t.Fatalf("unexpected Brier score: %f", report.BrierScore)
	}
	if report.LogLoss <= 0 || math.IsInf(report.LogLoss, 0) {
		t.Fatalf("unexpected log loss: %f", report.LogLoss)
	}
	if len(report.Buckets) != 10 {
		t.Fatalf("unexpected bucket count: %d", len(report.Buckets))
	}
	if math.Abs(report.Buckets[7].Gap-0.3) > 1e-9 {
		t.Fatalf("unexpected 0.7 bucket gap: %+v", report.Buckets[7])
	}
}

func TestCalibrationCSV(t *testing.T) {
	input := `timestamp,market_id,market_yes_price,model_yes_probability,resolved_outcome
2026-01-01T00:00:00Z,a,0.50,0.70,YES
2026-01-02T00:00:00Z,b,0.60,0.20,NO
`
	report, err := CalibrationCSV(strings.NewReader(input), 5)
	if err != nil {
		t.Fatal(err)
	}
	if report.Samples != 2 || report.BrierScore <= 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestPriceFeaturesAndMomentumRules(t *testing.T) {
	prices := make([]float64, 0, 60)
	for i := 0; i < 60; i++ {
		prices = append(prices, 0.50+float64(i)*0.002)
	}
	price := ComputePriceFeatures(prices, 6, 14, 50, 25)
	if price.Last <= 0 || price.ROC <= 0 || price.EMA <= 0 {
		t.Fatalf("unexpected price features: %+v", price)
	}
	if price.AroonOsc < 0 {
		t.Fatalf("rising series should have positive aroon: %+v", price)
	}

	book := OrderBookFeatures{
		BestBid:   0.61,
		BestAsk:   0.62,
		Spread:    0.01,
		SpreadPct: 0.01 / 0.615,
		AskDepth:  10000,
		BidDepth:  10000,
	}
	decision := EvaluateMomentumEntry(price, book, 100, 24*time.Hour, DefaultMomentumRules())
	if !decision.Enter {
		t.Fatalf("expected entry, got reasons: %v", decision.Reasons)
	}

	thin := EvaluateMomentumEntry(price, OrderBookFeatures{SpreadPct: 0.2, AskDepth: 1}, 100, 0, DefaultMomentumRules())
	if thin.Enter {
		t.Fatal("thin book / no time should be rejected")
	}
}
