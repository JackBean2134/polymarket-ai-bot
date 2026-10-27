package polymarket

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

type CalibrationSample struct {
	Timestamp   time.Time
	MarketID    string
	EventID     string
	MarketPrice float64
	Predicted   float64
	Outcome     float64
}

type CalibrationBucket struct {
	Lower        float64 `json:"lower"`
	Upper        float64 `json:"upper"`
	Count        int     `json:"count"`
	AvgPredicted float64 `json:"avg_predicted"`
	ObservedRate float64 `json:"observed_rate"`
	Gap          float64 `json:"gap"`
}

type CalibrationReport struct {
	Samples        int                 `json:"samples"`
	BrierScore     float64             `json:"brier_score"`
	LogLoss        float64             `json:"log_loss"`
	BaseRate       float64             `json:"base_rate"`
	MeanPrediction float64             `json:"mean_prediction"`
	Buckets        []CalibrationBucket `json:"buckets"`
}

func EvaluateCalibration(samples []CalibrationSample, bucketCount int) CalibrationReport {
	report := CalibrationReport{Samples: len(samples), Buckets: make([]CalibrationBucket, 0)}
	if len(samples) == 0 {
		return report
	}
	if bucketCount <= 0 {
		bucketCount = 10
	}

	type accumulator struct {
		count   int
		predSum float64
		outcome float64
	}
	buckets := make([]accumulator, bucketCount)
	brier := 0.0
	logLoss := 0.0
	predSum := 0.0
	outcomeSum := 0.0

	for _, sample := range samples {
		p := clampProbability(sample.Predicted)
		y := 0.0
		if sample.Outcome >= 0.5 {
			y = 1
		}
		brier += (p - y) * (p - y)
		pClamped := math.Max(1e-12, math.Min(1-1e-12, p))
		if y == 1 {
			logLoss -= math.Log(pClamped)
		} else {
			logLoss -= math.Log(1 - pClamped)
		}
		predSum += p
		outcomeSum += y

		index := int(p * float64(bucketCount))
		if index >= bucketCount {
			index = bucketCount - 1
		}
		buckets[index].count++
		buckets[index].predSum += p
		buckets[index].outcome += y
	}

	report.BrierScore = brier / float64(len(samples))
	report.LogLoss = logLoss / float64(len(samples))
	report.MeanPrediction = predSum / float64(len(samples))
	report.BaseRate = outcomeSum / float64(len(samples))

	for i, bucket := range buckets {
		lower := float64(i) / float64(bucketCount)
		upper := float64(i+1) / float64(bucketCount)
		entry := CalibrationBucket{Lower: lower, Upper: upper, Count: bucket.count}
		if bucket.count > 0 {
			entry.AvgPredicted = bucket.predSum / float64(bucket.count)
			entry.ObservedRate = bucket.outcome / float64(bucket.count)
			entry.Gap = entry.ObservedRate - entry.AvgPredicted
		}
		report.Buckets = append(report.Buckets, entry)
	}
	return report
}

// CalibrationCSV reads the same timestamped CSV as the backtest and reports
// probability calibration. It requires the prediction to have been recorded
// before resolution.
func CalibrationCSV(reader io.Reader, bucketCount int) (*CalibrationReport, error) {
	csvReader := csv.NewReader(reader)
	csvReader.TrimLeadingSpace = true
	header, err := csvReader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	columns := make(map[string]int, len(header))
	for i, name := range header {
		columns[strings.TrimSpace(name)] = i
	}
	required := []string{"timestamp", "market_id", "market_yes_price", "model_yes_probability", "resolved_outcome"}
	for _, name := range required {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("CSV is missing required column %q", name)
		}
	}

	var samples []CalibrationSample
	for rowNumber := 2; ; rowNumber++ {
		record, readErr := csvReader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read CSV row %d: %w", rowNumber, readErr)
		}
		if len(record) != len(header) {
			return nil, fmt.Errorf("CSV row %d has %d fields; want %d", rowNumber, len(record), len(header))
		}
		timestamp, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(record[columns["timestamp"]]))
		if parseErr != nil {
			return nil, fmt.Errorf("CSV row %d has invalid RFC3339 timestamp: %w", rowNumber, parseErr)
		}
		price, parseErr := parseBacktestProbability(record[columns["market_yes_price"]])
		if parseErr != nil || price <= 0 || price >= 1 {
			return nil, fmt.Errorf("CSV row %d market_yes_price must be between 0 and 1", rowNumber)
		}
		probability, parseErr := parseBacktestProbability(record[columns["model_yes_probability"]])
		if parseErr != nil || probability < 0 || probability > 1 {
			return nil, fmt.Errorf("CSV row %d model_yes_probability must be between 0 and 1", rowNumber)
		}
		outcome := strings.ToUpper(strings.TrimSpace(record[columns["resolved_outcome"]]))
		if outcome != "YES" && outcome != "NO" {
			return nil, fmt.Errorf("CSV row %d resolved_outcome must be YES or NO", rowNumber)
		}
		outcomeValue := 0.0
		if outcome == "YES" {
			outcomeValue = 1
		}
		samples = append(samples, CalibrationSample{
			Timestamp:   timestamp,
			MarketID:    strings.TrimSpace(record[columns["market_id"]]),
			MarketPrice: price,
			Predicted:   probability,
			Outcome:     outcomeValue,
		})
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("CSV contains no samples")
	}
	report := EvaluateCalibration(samples, bucketCount)
	return &report, nil
}
