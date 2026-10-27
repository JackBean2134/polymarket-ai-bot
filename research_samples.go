package polymarket

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SampleBuildOptions struct {
	DataDir         string
	ReportDir       string
	Offsets         []time.Duration
	MaxStaleness    time.Duration
	ROCLogitPeriod  int
	ATRPeriod       int
	EMAPeriod       int
	AroonPeriod     int
	MomentumWeight  float64
	HoldoutFraction float64
	Buckets         int
}

type SampleBuildSummary struct {
	Markets                 int                 `json:"markets"`
	Histories               int                 `json:"histories"`
	MarketSamples           int                 `json:"market_samples"`
	LogitSamples            int                 `json:"logit_samples"`
	Skipped                 int                 `json:"skipped"`
	MarketReport            *CalibrationReport  `json:"market_calibration"`
	LogitReport             *CalibrationReport  `json:"logit_calibration"`
	MarketHoldout           *CalibrationReport  `json:"market_holdout"`
	LogitHoldout            *CalibrationReport  `json:"logit_holdout"`
	BrierImprovement        float64             `json:"brier_improvement_logit_vs_market"`
	HoldoutBrierImprovement float64             `json:"holdout_brier_improvement_logit_vs_market"`
	SampleMarkets           int                 `json:"sample_markets"`
	Assessment              *ResearchAssessment `json:"assessment,omitempty"`
	MarketCSV               string              `json:"market_csv"`
	LogitCSV                string              `json:"logit_csv"`
	HoldoutMarketCSV        string              `json:"holdout_market_csv,omitempty"`
	HoldoutLogitCSV         string              `json:"holdout_logit_csv,omitempty"`
	ReportJSON              string              `json:"report_json"`
}

func BuildSamples(options SampleBuildOptions) (*SampleBuildSummary, error) {
	if options.DataDir == "" {
		options.DataDir = "data"
	}
	if options.ReportDir == "" {
		options.ReportDir = "reports"
	}
	if len(options.Offsets) == 0 {
		options.Offsets = []time.Duration{7 * 24 * time.Hour, 48 * time.Hour, 24 * time.Hour, 6 * time.Hour}
	}
	if options.MaxStaleness <= 0 {
		options.MaxStaleness = 6 * time.Hour
	}
	if options.ROCLogitPeriod <= 0 {
		options.ROCLogitPeriod = 6
	}
	if options.ATRPeriod <= 0 {
		options.ATRPeriod = 14
	}
	if options.EMAPeriod <= 0 {
		options.EMAPeriod = 50
	}
	if options.AroonPeriod <= 0 {
		options.AroonPeriod = 25
	}
	if options.MomentumWeight == 0 {
		options.MomentumWeight = 0.5
	}
	if options.HoldoutFraction <= 0 || options.HoldoutFraction >= 1 {
		options.HoldoutFraction = 0.3
	}
	if options.Buckets <= 0 {
		options.Buckets = 10
	}
	if err := os.MkdirAll(options.ReportDir, 0755); err != nil {
		return nil, fmt.Errorf("create report dir: %w", err)
	}

	markets, err := loadCollectedMarkets(filepath.Join(options.DataDir, "markets.jsonl"))
	if err != nil {
		return nil, err
	}
	histories, err := loadCollectedHistories(filepath.Join(options.DataDir, "price_history.jsonl"))
	if err != nil {
		return nil, err
	}

	var marketSamples []CalibrationSample
	var logitSamples []CalibrationSample
	summary := &SampleBuildSummary{Markets: len(markets), Histories: len(histories)}

	for _, market := range markets {
		history, ok := histories[market.MarketID]
		if !ok || len(history.Points) < 2 {
			summary.Skipped++
			continue
		}
		resolveTime := market.ClosedTime
		if resolveTime.IsZero() {
			resolveTime = market.EndDate
		}
		if resolveTime.IsZero() {
			summary.Skipped++
			continue
		}
		outcome := 0.0
		if strings.EqualFold(market.ResolvedOutcome, "YES") {
			outcome = 1
		} else if !strings.EqualFold(market.ResolvedOutcome, "NO") {
			summary.Skipped++
			continue
		}
		points := history.Points
		sort.SliceStable(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
		seenPoints := make(map[string]struct{})

		for _, offset := range options.Offsets {
			target := resolveTime.Add(-offset)
			point, ok := priceAt(points, target, options.MaxStaleness)
			if !ok {
				summary.Skipped++
				continue
			}
			sampleKey := point.Timestamp.UTC().Format(time.RFC3339Nano)
			if _, exists := seenPoints[sampleKey]; exists {
				continue
			}
			seenPoints[sampleKey] = struct{}{}
			price := point.Price
			if price <= 0 || price >= 1 {
				summary.Skipped++
				continue
			}
			pastPrices := pricesUpTo(points, point.Timestamp)
			features := ComputePriceFeatures(pastPrices, options.ROCLogitPeriod, options.ATRPeriod, options.EMAPeriod, options.AroonPeriod)
			if features.SampleCount <= options.ROCLogitPeriod+1 {
				summary.Skipped++
				continue
			}
			modelProbability := sigmoid(features.Logit + options.MomentumWeight*features.ROC)
			marketSamples = append(marketSamples, CalibrationSample{
				Timestamp:   point.Timestamp,
				MarketID:    market.MarketID,
				EventID:     market.EventID,
				MarketPrice: price,
				Predicted:   price,
				Outcome:     outcome,
			})
			logitSamples = append(logitSamples, CalibrationSample{
				Timestamp:   point.Timestamp,
				MarketID:    market.MarketID,
				EventID:     market.EventID,
				MarketPrice: price,
				Predicted:   modelProbability,
				Outcome:     outcome,
			})
		}
	}

	marketCSV := filepath.Join(options.ReportDir, "samples_market.csv")
	logitCSV := filepath.Join(options.ReportDir, "samples_logit.csv")
	if len(marketSamples) == 0 {
		return nil, fmt.Errorf("no usable samples; check that collected markets have enough pre-close history and valid settled outcomes")
	}
	if err := writeSamplesCSV(marketCSV, marketSamples, markets); err != nil {
		return nil, err
	}
	if err := writeSamplesCSV(logitCSV, logitSamples, markets); err != nil {
		return nil, err
	}

	marketReport := EvaluateCalibration(marketSamples, options.Buckets)
	logitReport := EvaluateCalibration(logitSamples, options.Buckets)
	summary.MarketSamples = len(marketSamples)
	summary.LogitSamples = len(logitSamples)
	summary.SampleMarkets = countSampleMarkets(marketSamples)
	summary.MarketReport = &marketReport
	summary.LogitReport = &logitReport
	summary.BrierImprovement = marketReport.BrierScore - logitReport.BrierScore

	holdoutIDs := selectMarketHoldout(markets, marketSamples, options.HoldoutFraction)
	var holdoutMarketSamples []CalibrationSample
	var holdoutLogitSamples []CalibrationSample
	for i, sample := range marketSamples {
		if _, ok := holdoutIDs[sample.MarketID]; !ok {
			continue
		}
		holdoutMarketSamples = append(holdoutMarketSamples, sample)
		holdoutLogitSamples = append(holdoutLogitSamples, logitSamples[i])
	}
	if len(holdoutMarketSamples) > 0 {
		marketHoldout := EvaluateCalibration(holdoutMarketSamples, options.Buckets)
		logitHoldout := EvaluateCalibration(holdoutLogitSamples, options.Buckets)
		summary.MarketHoldout = &marketHoldout
		summary.LogitHoldout = &logitHoldout
		summary.HoldoutBrierImprovement = marketHoldout.BrierScore - logitHoldout.BrierScore
		summary.HoldoutMarketCSV = filepath.Join(options.ReportDir, "samples_market_holdout.csv")
		summary.HoldoutLogitCSV = filepath.Join(options.ReportDir, "samples_logit_holdout.csv")
		if err := writeSamplesCSV(summary.HoldoutMarketCSV, holdoutMarketSamples, markets); err != nil {
			return nil, err
		}
		if err := writeSamplesCSV(summary.HoldoutLogitCSV, holdoutLogitSamples, markets); err != nil {
			return nil, err
		}
		if err := writeJSONFile(filepath.Join(options.ReportDir, "calibration_market_holdout.json"), marketHoldout); err != nil {
			return nil, err
		}
		if err := writeJSONFile(filepath.Join(options.ReportDir, "calibration_logit_holdout.json"), logitHoldout); err != nil {
			return nil, err
		}
	}
	summary.MarketCSV = marketCSV
	summary.LogitCSV = logitCSV
	summary.ReportJSON = filepath.Join(options.ReportDir, "sample_summary.json")
	summary.Assessment = AssessSamples(marketSamples, logitSamples, holdoutMarketSamples, holdoutLogitSamples)
	summary.Assessment.MarkdownPath = filepath.Join(options.ReportDir, "research_summary.md")

	if err := writeJSONFile(summary.ReportJSON, summary); err != nil {
		return nil, err
	}
	if err := writeResearchMarkdown(summary.Assessment.MarkdownPath, summary); err != nil {
		return nil, err
	}
	if err := writeJSONFile(filepath.Join(options.ReportDir, "calibration_market.json"), marketReport); err != nil {
		return nil, err
	}
	if err := writeJSONFile(filepath.Join(options.ReportDir, "calibration_logit.json"), logitReport); err != nil {
		return nil, err
	}
	return summary, nil
}

func priceAt(points []PriceHistoryPoint, target time.Time, maxStaleness time.Duration) (PriceHistoryPoint, bool) {
	var best PriceHistoryPoint
	found := false
	for _, point := range points {
		if point.Timestamp.After(target) {
			break
		}
		if point.Price <= 0 || point.Price >= 1 {
			continue
		}
		best = point
		found = true
	}
	if !found || target.Sub(best.Timestamp) > maxStaleness {
		return PriceHistoryPoint{}, false
	}
	return best, true
}

func pricesUpTo(points []PriceHistoryPoint, timestamp time.Time) []float64 {
	prices := make([]float64, 0)
	for _, point := range points {
		if point.Timestamp.After(timestamp) {
			break
		}
		if point.Price > 0 && point.Price < 1 {
			prices = append(prices, point.Price)
		}
	}
	return prices
}

func sigmoid(value float64) float64 {
	if value > 50 {
		return 1 - 1e-12
	}
	if value < -50 {
		return 1e-12
	}
	return 1 / (1 + math.Exp(-value))
}

func loadCollectedMarkets(path string) ([]CollectedMarket, error) {
	var markets []CollectedMarket
	seen := make(map[string]int)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var market CollectedMarket
		if err := json.Unmarshal([]byte(line), &market); err != nil {
			continue
		}
		if market.MarketID == "" {
			continue
		}
		if index, ok := seen[market.MarketID]; ok {
			markets[index] = market
			continue
		}
		seen[market.MarketID] = len(markets)
		markets = append(markets, market)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return markets, nil
}

func loadCollectedHistories(path string) (map[string]CollectedHistory, error) {
	histories := make(map[string]CollectedHistory)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var history CollectedHistory
		if err := json.Unmarshal([]byte(line), &history); err != nil {
			continue
		}
		if history.MarketID != "" {
			histories[history.MarketID] = history
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return histories, nil
}

func selectMarketHoldout(markets []CollectedMarket, samples []CalibrationSample, fraction float64) map[string]struct{} {
	holdout := make(map[string]struct{})
	if fraction <= 0 || fraction >= 1 || len(samples) == 0 {
		return holdout
	}
	marketByID := make(map[string]CollectedMarket, len(markets))
	for _, market := range markets {
		marketByID[market.MarketID] = market
	}
	sampleLatest := make(map[string]time.Time)
	for _, sample := range samples {
		latest := sampleLatest[sample.MarketID]
		if sample.Timestamp.After(latest) {
			sampleLatest[sample.MarketID] = sample.Timestamp
		}
	}

	type group struct {
		key       string
		marketIDs []string
		resolve   time.Time
	}
	groups := make(map[string]*group)
	for marketID, latest := range sampleLatest {
		market := marketByID[marketID]
		key := market.EventID
		if key == "" {
			key = marketID
		}
		entry := groups[key]
		if entry == nil {
			entry = &group{key: key}
			groups[key] = entry
		}
		entry.marketIDs = append(entry.marketIDs, marketID)
		resolve := market.ClosedTime
		if resolve.IsZero() {
			resolve = market.EndDate
		}
		if resolve.IsZero() {
			resolve = latest
		}
		if resolve.After(entry.resolve) {
			entry.resolve = resolve
		}
	}
	if len(groups) < 2 {
		return holdout
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := groups[keys[i]], groups[keys[j]]
		if left.resolve.Equal(right.resolve) {
			return left.key < right.key
		}
		return left.resolve.Before(right.resolve)
	})
	count := int(math.Ceil(float64(len(keys)) * fraction))
	if count < 1 {
		count = 1
	}
	if count >= len(keys) {
		count = len(keys) - 1
	}
	for _, key := range keys[len(keys)-count:] {
		for _, marketID := range groups[key].marketIDs {
			holdout[marketID] = struct{}{}
		}
	}
	return holdout
}

func writeSamplesCSV(path string, samples []CalibrationSample, markets []CollectedMarket) error {
	titles := make(map[string]string, len(markets))
	for _, market := range markets {
		titles[market.MarketID] = market.Title
	}
	sort.SliceStable(samples, func(i, j int) bool {
		return samples[i].Timestamp.Before(samples[j].Timestamp)
	})

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	if err := writer.Write([]string{
		"timestamp", "market_id", "market_yes_price", "model_yes_probability", "resolved_outcome", "market_title",
	}); err != nil {
		return err
	}
	for _, sample := range samples {
		outcome := "NO"
		if sample.Outcome >= 0.5 {
			outcome = "YES"
		}
		if err := writer.Write([]string{
			sample.Timestamp.UTC().Format(time.RFC3339),
			sample.MarketID,
			strconv.FormatFloat(sample.MarketPrice, 'f', -1, 64),
			strconv.FormatFloat(sample.Predicted, 'f', -1, 64),
			outcome,
			titles[sample.MarketID],
		}); err != nil {
			return err
		}
	}
	return writer.Error()
}

func writeJSONFile(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
