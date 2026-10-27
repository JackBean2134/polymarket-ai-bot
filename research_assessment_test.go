package polymarket

import (
	"fmt"
	"testing"
)

func TestAssessSamplesRequiresEnoughMarkets(t *testing.T) {
	samples := make([]CalibrationSample, 10)
	model := make([]CalibrationSample, 10)
	for i := range samples {
		samples[i] = CalibrationSample{MarketID: fmt.Sprintf("m%d", i), Timestamp: samples[i].Timestamp, Predicted: 0.5, Outcome: float64(i % 2)}
		model[i] = samples[i]
		model[i].Predicted = 0.6
	}
	assessment := AssessSamples(samples, model, nil, nil)
	if assessment.Decision != "INSUFFICIENT_DATA" {
		t.Fatalf("expected insufficient data, got %+v", assessment)
	}
}

func TestAssessSamplesPromisingWithEnoughMarkets(t *testing.T) {
	market := make([]CalibrationSample, 0, researchMinMarkets)
	model := make([]CalibrationSample, 0, researchMinMarkets)
	for i := 0; i < researchMinMarkets; i++ {
		outcome := float64(i % 2)
		market = append(market, CalibrationSample{MarketID: fmt.Sprintf("m%d", i), Predicted: 0.5, Outcome: outcome})
		prediction := 0.6
		if outcome == 0 {
			prediction = 0.4
		}
		model = append(model, CalibrationSample{MarketID: fmt.Sprintf("m%d", i), Predicted: prediction, Outcome: outcome})
	}
	assessment := AssessSamples(market, model, market[210:], model[210:])
	if assessment.Decision != "PROMISING" {
		t.Fatalf("expected promising, got %+v", assessment)
	}
	if assessment.HoldoutBootstrap == nil || assessment.HoldoutBootstrap.Lower95 <= 0 {
		t.Fatalf("expected positive holdout interval, got %+v", assessment)
	}
}
