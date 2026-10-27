package polymarket

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveBinaryOutcome(t *testing.T) {
	market := &Market{Closed: true, Outcomes: []string{"Yes", "No"}, OutcomePrices: []float64{1, 0}}
	outcome, err := ResolveBinaryOutcome(market)
	if err != nil || outcome != "YES" {
		t.Fatalf("unexpected resolution: %s %v", outcome, err)
	}
	market.OutcomePrices = []float64{0, 1}
	outcome, err = ResolveBinaryOutcome(market)
	if err != nil || outcome != "NO" {
		t.Fatalf("unexpected resolution: %s %v", outcome, err)
	}
	market.OutcomePrices = []float64{0.6, 0.4}
	if _, err := ResolveBinaryOutcome(market); err == nil {
		t.Fatal("ambiguous prices must be rejected")
	}
}

func TestSelectMarketHoldoutKeepsEventTogether(t *testing.T) {
	now := time.Now().UTC()
	markets := []CollectedMarket{
		{MarketID: "a", EventID: "e1", ClosedTime: now.Add(-3 * time.Hour)},
		{MarketID: "b", EventID: "e1", ClosedTime: now.Add(-3 * time.Hour)},
		{MarketID: "c", EventID: "e2", ClosedTime: now.Add(-2 * time.Hour)},
		{MarketID: "d", EventID: "e3", ClosedTime: now.Add(-1 * time.Hour)},
	}
	samples := []CalibrationSample{
		{MarketID: "a", Timestamp: now.Add(-4 * time.Hour)},
		{MarketID: "b", Timestamp: now.Add(-4 * time.Hour)},
		{MarketID: "c", Timestamp: now.Add(-3 * time.Hour)},
		{MarketID: "d", Timestamp: now.Add(-2 * time.Hour)},
	}
	holdout := selectMarketHoldout(markets, samples, 0.34)
	if _, ok := holdout["a"]; ok {
		t.Fatal("event e1 must not be split")
	}
	if _, ok := holdout["b"]; ok {
		t.Fatal("event e1 must not be split")
	}
	if _, ok := holdout["c"]; !ok {
		t.Fatal("latest event e2 should be in holdout")
	}
	if _, ok := holdout["d"]; !ok {
		t.Fatal("latest event e3 should be in holdout")
	}
}

func TestBuildSamplesFromCollectedData(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	reportDir := filepath.Join(dir, "reports")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	market := CollectedMarket{
		MarketID:        "m1",
		Title:           "test market",
		ResolvedOutcome: "YES",
		ClosedTime:      now,
	}
	if err := appendJSONLine(filepath.Join(dataDir, "markets.jsonl"), market); err != nil {
		t.Fatal(err)
	}

	points := make([]PriceHistoryPoint, 0, 30)
	for i := 0; i < 30; i++ {
		points = append(points, PriceHistoryPoint{
			Timestamp: now.Add(-3*time.Hour + time.Duration(i)*6*time.Minute),
			Price:     0.40 + float64(i)*0.005,
		})
	}
	if err := appendJSONLine(filepath.Join(dataDir, "price_history.jsonl"), CollectedHistory{
		MarketID:   "m1",
		YesTokenID: "yes",
		Points:     points,
	}); err != nil {
		t.Fatal(err)
	}

	summary, err := BuildSamples(SampleBuildOptions{
		DataDir:        dataDir,
		ReportDir:      reportDir,
		Offsets:        []time.Duration{2 * time.Hour},
		MaxStaleness:   time.Hour,
		MomentumWeight: 0.5,
		Buckets:        5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.MarketSamples != 1 || summary.LogitSamples != 1 {
		t.Fatalf("unexpected sample counts: %+v", summary)
	}
	if summary.MarketReport == nil || summary.LogitReport == nil {
		t.Fatalf("missing calibration reports: %+v", summary)
	}
	for _, path := range []string{summary.MarketCSV, summary.LogitCSV, summary.ReportJSON} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing output %s: %v", path, err)
		}
	}
}
