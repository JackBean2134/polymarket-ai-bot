package polymarket

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
)

const (
	researchMinMarkets        = 300
	researchMinHoldoutMarkets = 75
	researchBootstrapRuns     = 2000
	researchBootstrapSeed     = int64(20261027)
)

type BootstrapInterval struct {
	Markets              int     `json:"markets"`
	Clusters             int     `json:"clusters"`
	Iterations           int     `json:"iterations"`
	MeanBrierImprovement float64 `json:"mean_brier_improvement"`
	Lower95              float64 `json:"lower_95"`
	Upper95              float64 `json:"upper_95"`
}

type ResearchAssessment struct {
	Decision          string             `json:"decision"`
	Reasons           []string           `json:"reasons"`
	SampleMarkets     int                `json:"sample_markets"`
	HoldoutMarkets    int                `json:"holdout_markets"`
	MinMarkets        int                `json:"min_markets"`
	MinHoldoutMarkets int                `json:"min_holdout_markets"`
	FullBootstrap     *BootstrapInterval `json:"full_bootstrap,omitempty"`
	HoldoutBootstrap  *BootstrapInterval `json:"holdout_bootstrap,omitempty"`
	MarkdownPath      string             `json:"markdown_path,omitempty"`
}

func AssessSamples(marketSamples, logitSamples, holdoutMarketSamples, holdoutLogitSamples []CalibrationSample) *ResearchAssessment {
	assessment := &ResearchAssessment{
		Decision:          "INSUFFICIENT_DATA",
		MinMarkets:        researchMinMarkets,
		MinHoldoutMarkets: researchMinHoldoutMarkets,
	}
	if len(marketSamples) != len(logitSamples) || len(marketSamples) == 0 {
		assessment.Reasons = append(assessment.Reasons, "market and model samples are not aligned")
		return assessment
	}
	assessment.SampleMarkets = countSampleMarkets(marketSamples)
	assessment.FullBootstrap = bootstrapImprovement(marketSamples, logitSamples)
	if len(holdoutMarketSamples) > 0 && len(holdoutMarketSamples) == len(holdoutLogitSamples) {
		assessment.HoldoutMarkets = countSampleMarkets(holdoutMarketSamples)
		assessment.HoldoutBootstrap = bootstrapImprovement(holdoutMarketSamples, holdoutLogitSamples)
	}

	if assessment.SampleMarkets < researchMinMarkets {
		assessment.Reasons = append(assessment.Reasons,
			fmt.Sprintf("only %d markets with usable samples; need at least %d", assessment.SampleMarkets, researchMinMarkets))
	}
	if assessment.HoldoutMarkets < researchMinHoldoutMarkets {
		assessment.Reasons = append(assessment.Reasons,
			fmt.Sprintf("only %d holdout markets; need at least %d", assessment.HoldoutMarkets, researchMinHoldoutMarkets))
	}
	if assessment.SampleMarkets < researchMinMarkets || assessment.HoldoutMarkets < researchMinHoldoutMarkets ||
		assessment.HoldoutBootstrap == nil || assessment.FullBootstrap == nil {
		return assessment
	}
	if assessment.FullBootstrap.MeanBrierImprovement <= 0 {
		assessment.Reasons = append(assessment.Reasons, "model does not beat the market baseline on the full sample")
	}
	if assessment.HoldoutBootstrap.MeanBrierImprovement <= 0 {
		assessment.Reasons = append(assessment.Reasons, "model does not beat the market baseline on the event-group holdout")
	}
	if assessment.HoldoutBootstrap.Lower95 <= 0 {
		assessment.Reasons = append(assessment.Reasons, "holdout bootstrap 95% interval includes zero")
	}
	if len(assessment.Reasons) > 0 {
		assessment.Decision = "NO_EDGE"
		return assessment
	}
	assessment.Decision = "PROMISING"
	assessment.Reasons = append(assessment.Reasons, "market-level holdout improvement is positive and its bootstrap interval excludes zero")
	return assessment
}

func countSampleMarkets(samples []CalibrationSample) int {
	seen := make(map[string]struct{}, len(samples))
	for _, sample := range samples {
		seen[sample.MarketID] = struct{}{}
	}
	return len(seen)
}

func writeResearchMarkdown(path string, summary *SampleBuildSummary) error {
	if summary == nil {
		return fmt.Errorf("summary is nil")
	}
	var builder strings.Builder
	builder.WriteString("# Research assessment\n\n")
	builder.WriteString("This report is generated from settled markets and pre-close prices. It is research, not a profitability guarantee.\n\n")
	fmt.Fprintf(&builder, "- Markets in data: %d\n", summary.Markets)
	fmt.Fprintf(&builder, "- Markets with samples: %d\n", summary.SampleMarkets)
	fmt.Fprintf(&builder, "- Sample decisions: %d\n", summary.MarketSamples)
	if summary.MarketReport != nil && summary.LogitReport != nil {
		builder.WriteString("\n## Full sample\n\n")
		builder.WriteString("| Model | Brier | Log loss | Samples |\n|---|---:|---:|---:|\n")
		fmt.Fprintf(&builder, "| Market price | %.6f | %.6f | %d |\n", summary.MarketReport.BrierScore, summary.MarketReport.LogLoss, summary.MarketReport.Samples)
		fmt.Fprintf(&builder, "| Logit momentum | %.6f | %.6f | %d |\n", summary.LogitReport.BrierScore, summary.LogitReport.LogLoss, summary.LogitReport.Samples)
	}
	if summary.MarketHoldout != nil && summary.LogitHoldout != nil {
		builder.WriteString("\n## Event-group holdout\n\n")
		builder.WriteString("| Model | Brier | Log loss | Samples |\n|---|---:|---:|---:|\n")
		fmt.Fprintf(&builder, "| Market price | %.6f | %.6f | %d |\n", summary.MarketHoldout.BrierScore, summary.MarketHoldout.LogLoss, summary.MarketHoldout.Samples)
		fmt.Fprintf(&builder, "| Logit momentum | %.6f | %.6f | %d |\n", summary.LogitHoldout.BrierScore, summary.LogitHoldout.LogLoss, summary.LogitHoldout.Samples)
	}
	if summary.Assessment != nil {
		assessment := summary.Assessment
		builder.WriteString("\n## Decision\n\n")
		fmt.Fprintf(&builder, "**%s**\n\n", assessment.Decision)
		for _, reason := range assessment.Reasons {
			fmt.Fprintf(&builder, "- %s\n", reason)
		}
		if assessment.FullBootstrap != nil {
			builder.WriteString("\n## Event-cluster bootstrap\n\n")
			builder.WriteString("Positive improvement means the model has lower Brier than the market price.\n\n")
			builder.WriteString("| Window | Markets | Event clusters | Mean Brier improvement | 95% interval |\n|---|---:|---:|---:|---:|\n")
			fmt.Fprintf(&builder, "| Full | %d | %d | %.6f | [%.6f, %.6f] |\n",
				assessment.FullBootstrap.Markets, assessment.FullBootstrap.Clusters, assessment.FullBootstrap.MeanBrierImprovement,
				assessment.FullBootstrap.Lower95, assessment.FullBootstrap.Upper95)
			if assessment.HoldoutBootstrap != nil {
				fmt.Fprintf(&builder, "| Holdout | %d | %d | %.6f | [%.6f, %.6f] |\n",
					assessment.HoldoutBootstrap.Markets, assessment.HoldoutBootstrap.Clusters, assessment.HoldoutBootstrap.MeanBrierImprovement,
					assessment.HoldoutBootstrap.Lower95, assessment.HoldoutBootstrap.Upper95)
			}
		}
	}
	builder.WriteString("\n## Required next checks\n\n")
	builder.WriteString("- Keep trading disabled until the holdout decision is PROMISING with enough markets.\n")
	builder.WriteString("- Run the offline backtest with realistic fees and slippage.\n")
	builder.WriteString("- Paper trade for at least two weeks and verify fills, fees and settlement before risking capital.\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func bootstrapImprovement(marketSamples, logitSamples []CalibrationSample) *BootstrapInterval {
	if len(marketSamples) == 0 || len(marketSamples) != len(logitSamples) {
		return nil
	}
	type marketAggregate struct {
		marketBrier float64
		modelBrier  float64
		count       int
	}
	byMarket := make(map[string]*marketAggregate)
	for i, sample := range marketSamples {
		key := "market:" + sample.MarketID
		if sample.EventID != "" {
			key = "event:" + sample.EventID
		}
		entry := byMarket[key]
		if entry == nil {
			entry = &marketAggregate{}
			byMarket[key] = entry
		}
		entry.marketBrier += brierScore(sample.Predicted, sample.Outcome)
		entry.modelBrier += brierScore(logitSamples[i].Predicted, sample.Outcome)
		entry.count++
	}
	keys := make([]string, 0, len(byMarket))
	for key := range byMarket {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	deltas := make([]float64, 0, len(keys))
	for _, key := range keys {
		entry := byMarket[key]
		deltas = append(deltas, (entry.marketBrier-entry.modelBrier)/float64(entry.count))
	}
	if len(deltas) == 0 {
		return nil
	}
	mean := 0.0
	for _, delta := range deltas {
		mean += delta
	}
	mean /= float64(len(deltas))

	rng := rand.New(rand.NewSource(researchBootstrapSeed))
	means := make([]float64, 0, researchBootstrapRuns)
	for i := 0; i < researchBootstrapRuns; i++ {
		total := 0.0
		for j := 0; j < len(deltas); j++ {
			total += deltas[rng.Intn(len(deltas))]
		}
		means = append(means, total/float64(len(deltas)))
	}
	sort.Float64s(means)
	return &BootstrapInterval{
		Markets:              countSampleMarkets(marketSamples),
		Clusters:             len(deltas),
		Iterations:           researchBootstrapRuns,
		MeanBrierImprovement: mean,
		Lower95:              researchPercentile(means, 0.025),
		Upper95:              researchPercentile(means, 0.975),
	}
}

func brierScore(probability, outcome float64) float64 {
	delta := probability - outcome
	return delta * delta
}

func researchPercentile(sortedValues []float64, q float64) float64 {
	if len(sortedValues) == 0 {
		return math.NaN()
	}
	index := int(q * float64(len(sortedValues)-1))
	if index < 0 {
		index = 0
	}
	if index >= len(sortedValues) {
		index = len(sortedValues) - 1
	}
	return sortedValues[index]
}
