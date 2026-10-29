package polymarket

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	// minProjectionHours is the shortest window that may be daily-ised.
	minProjectionHours = 24.0
	// stalledCycleMinutes flags a suspended process or stalled scan.
	stalledCycleMinutes = 15.0
)

// AutopilotLogSummary is the offline analysis of an -autopilot-out JSONL file.
// Every money field comes from the paper simulation: LockedProfitUSD is edge
// locked in complete baskets, not cash, and only settles when the market does.
type AutopilotLogSummary struct {
	Records      int       `json:"records"`
	SkippedLines int       `json:"skipped_lines"`
	Cycles       int       `json:"cycles"`
	ScansFailed  int       `json:"scans_failed"`
	FirstCycleAt time.Time `json:"first_cycle_at,omitempty"`
	LastCycleAt  time.Time `json:"last_cycle_at,omitempty"`
	// ObservationHours is the wall-clock span between the first cycle start
	// and the last cycle finish.
	ObservationHours float64 `json:"observation_hours"`
	// LongestCycleMinutes flags suspended processes and stalled scans, which
	// inflate ObservationHours and understate every per-hour rate.
	LongestCycleMinutes float64  `json:"longest_cycle_minutes"`
	DataQualityWarnings []string `json:"data_quality_warnings,omitempty"`

	EventsScanned           int     `json:"events_scanned"`
	OpportunitiesSeen       int     `json:"opportunities_seen"`
	CyclesWithOpportunities int     `json:"cycles_with_opportunities"`
	DistinctEvents          int     `json:"distinct_events"`
	CycleHitRatePct         float64 `json:"cycle_hit_rate_pct"`
	OpportunitiesPerHour    float64 `json:"opportunities_per_hour"`

	BestNetEdgeUSD       float64 `json:"best_net_edge_usd"`
	MedianNetEdgeUSD     float64 `json:"median_net_edge_usd"`
	MeanNetEdgeUSD       float64 `json:"mean_net_edge_usd"`
	AvailableNotionalUSD float64 `json:"available_notional_usd"`

	BasketsFilled   int     `json:"baskets_filled"`
	BasketsAborted  int     `json:"baskets_aborted"`
	DeployedUSD     float64 `json:"deployed_usd"`
	FeesPaidUSD     float64 `json:"fees_paid_usd"`
	LockedProfitUSD float64 `json:"locked_profit_usd"`
	NakedLegsUSD    float64 `json:"naked_legs_usd"`

	ProfitPerHourUSD        float64 `json:"profit_per_hour_usd"`
	ProjectedDailyProfitUSD float64 `json:"projected_daily_profit_usd"`
	ProjectedDailyReturnPct float64 `json:"projected_daily_return_pct"`
	// ProjectionWarning is set when the observed window is too short to
	// annualise/daily-ise without producing a fantasy number.
	ProjectionWarning string   `json:"projection_warning,omitempty"`
	HaltReasons       []string `json:"halt_reasons,omitempty"`

	Verdict string `json:"verdict"`
	Note    string `json:"note"`

	// TopCategory is the category with the most scanned opportunities.
	TopCategory string                  `json:"top_category,omitempty"`
	ByCategory  []AutopilotCategoryStat `json:"by_category,omitempty"`
	ByHourUTC   []AutopilotHourStat     `json:"by_hour_utc,omitempty"`
}

// AutopilotCategoryStat groups scanned opportunities and filled baskets by
// event category (Gamma category, else the canonical tag bucket, else
// "uncategorized").
type AutopilotCategoryStat struct {
	Key                  string  `json:"key"`
	Opportunities        int     `json:"opportunities"`
	OpportunitySharePct  float64 `json:"opportunity_share_pct"`
	DistinctEvents       int     `json:"distinct_events"`
	BestNetEdgeUSD       float64 `json:"best_net_edge_usd"`
	MedianNetEdgeUSD     float64 `json:"median_net_edge_usd"`
	AvailableNotionalUSD float64 `json:"available_notional_usd"`
	BasketsFilled        int     `json:"baskets_filled"`
	LockedProfitUSD      float64 `json:"locked_profit_usd"`
}

// AutopilotHourStat groups cycles by the UTC hour in which they started.
type AutopilotHourStat struct {
	HourUTC                 int     `json:"hour_utc"`
	Cycles                  int     `json:"cycles"`
	CyclesWithOpportunities int     `json:"cycles_with_opportunities"`
	Opportunities           int     `json:"opportunities"`
	BasketsFilled           int     `json:"baskets_filled"`
	LockedProfitUSD         float64 `json:"locked_profit_usd"`
	BestNetEdgeUSD          float64 `json:"best_net_edge_usd"`
}

type autopilotCategoryAcc struct {
	edges         []float64
	events        map[string]struct{}
	opportunities int
	notional      float64
	baskets       int
	locked        float64
}

type autopilotHourAcc struct {
	cycles                  int
	cyclesWithOpportunities int
	opportunities           int
	baskets                 int
	locked                  float64
	best                    float64
}

type autopilotLogRecord struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Reason    string          `json:"reason"`
	State     json.RawMessage `json:"state"`
}

// SummarizeAutopilotLogFile reads a JSONL decision log written by -autopilot-out.
func SummarizeAutopilotLogFile(path string) (*AutopilotLogSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open autopilot log: %w", err)
	}
	defer file.Close()

	return SummarizeAutopilotLog(file)
}

// SummarizeAutopilotLog aggregates cycles, candidates, fills and aborts. Bad
// lines are counted and skipped so a single truncated write does not destroy a
// week of data.
func SummarizeAutopilotLog(reader io.Reader) (*AutopilotLogSummary, error) {
	if reader == nil {
		return nil, fmt.Errorf("autopilot log reader is nil")
	}

	summary := &AutopilotLogSummary{}
	edges := make([]float64, 0)
	events := make(map[string]struct{})
	halts := make(map[string]struct{})
	categories := make(map[string]*autopilotCategoryAcc)
	hours := make(map[int]*autopilotHourAcc)
	eventCategories := make(map[string]string)
	var started, finished time.Time

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record autopilotLogRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			summary.SkippedLines++
			continue
		}
		// Only autopilot records count; unrelated JSON in the file is skipped
		// so a shared log cannot silently appear valid.
		if !isAutopilotRecordType(record.Type) {
			summary.SkippedLines++
			continue
		}

		switch record.Type {
		case "autopilot_cycle":
			var cycle AutopilotCycle
			if len(record.State) == 0 || json.Unmarshal(record.State, &cycle) != nil {
				summary.SkippedLines++
				continue
			}
			summary.Records++
			summary.Cycles++
			summary.EventsScanned += cycle.EventsScanned
			summary.OpportunitiesSeen += cycle.Opportunities
			if cycle.Opportunities > 0 {
				summary.CyclesWithOpportunities++
			}
			if !cycle.StartedAt.IsZero() && (started.IsZero() || cycle.StartedAt.Before(started)) {
				started = cycle.StartedAt
			}
			if !cycle.FinishedAt.IsZero() && cycle.FinishedAt.After(finished) {
				finished = cycle.FinishedAt
			}
			if !cycle.StartedAt.IsZero() && cycle.FinishedAt.After(cycle.StartedAt) {
				minutes := cycle.FinishedAt.Sub(cycle.StartedAt).Minutes()
				if minutes > summary.LongestCycleMinutes {
					summary.LongestCycleMinutes = minutes
				}
				if minutes > stalledCycleMinutes {
					summary.DataQualityWarnings = append(summary.DataQualityWarnings, fmt.Sprintf(
						"cycle %d took %.0f minutes; a sleeping machine or stalled scan inflates observation_hours and understates rates", cycle.Index, minutes))
				}
			}
			if !cycle.StartedAt.IsZero() {
				hour := cycle.StartedAt.UTC().Hour()
				acc := hours[hour]
				if acc == nil {
					acc = &autopilotHourAcc{}
					hours[hour] = acc
				}
				acc.cycles++
				if cycle.Opportunities > 0 {
					acc.cyclesWithOpportunities++
				}
				acc.opportunities += cycle.Opportunities
			}
			for _, candidate := range cycle.Candidates {
				edges = append(edges, candidate.NetEdgeUSD)
				if candidate.EventID != "" {
					events[candidate.EventID] = struct{}{}
				}
				summary.AvailableNotionalUSD += candidate.MaxBaskets * candidate.CostUSD

				key := categoryKey(candidate.Category)
				if candidate.EventID != "" {
					eventCategories[candidate.EventID] = key
				}
				category := categories[key]
				if category == nil {
					category = &autopilotCategoryAcc{events: make(map[string]struct{})}
					categories[key] = category
				}
				category.opportunities++
				category.edges = append(category.edges, candidate.NetEdgeUSD)
				category.notional += candidate.MaxBaskets * candidate.CostUSD
				if candidate.EventID != "" {
					category.events[candidate.EventID] = struct{}{}
				}
				if !cycle.StartedAt.IsZero() {
					hour := cycle.StartedAt.UTC().Hour()
					acc := hours[hour]
					if acc == nil {
						acc = &autopilotHourAcc{}
						hours[hour] = acc
					}
					if candidate.NetEdgeUSD > acc.best {
						acc.best = candidate.NetEdgeUSD
					}
				}
			}
		case "autopilot_basket":
			var basket AutopilotBasket
			if len(record.State) == 0 || json.Unmarshal(record.State, &basket) != nil {
				summary.SkippedLines++
				continue
			}
			summary.Records++
			summary.BasketsFilled++
			summary.DeployedUSD += basket.CostUSD + basket.FeeUSD
			summary.FeesPaidUSD += basket.FeeUSD
			summary.LockedProfitUSD += basket.LockedProfitUSD

			key := strings.TrimSpace(basket.Category)
			if key == "" {
				key = eventCategories[basket.EventID]
			}
			key = categoryKey(key)
			category := categories[key]
			if category == nil {
				category = &autopilotCategoryAcc{events: make(map[string]struct{})}
				categories[key] = category
			}
			category.baskets++
			category.locked += basket.LockedProfitUSD

			if !basket.FilledAt.IsZero() {
				hour := basket.FilledAt.UTC().Hour()
				acc := hours[hour]
				if acc == nil {
					acc = &autopilotHourAcc{}
					hours[hour] = acc
				}
				acc.baskets++
				acc.locked += basket.LockedProfitUSD
			}
		case "autopilot_abort":
			var abort AutopilotAbort
			if len(record.State) == 0 || json.Unmarshal(record.State, &abort) != nil {
				summary.SkippedLines++
				continue
			}
			summary.Records++
			summary.BasketsAborted++
			summary.NakedLegsUSD += abort.NakedNotionalUSD
		case "autopilot_scan_error":
			summary.Records++
			summary.ScansFailed++
		case "autopilot_halt":
			summary.Records++
			if reason := strings.TrimSpace(record.Reason); reason != "" {
				halts[reason] = struct{}{}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read autopilot log: %w", err)
	}
	if summary.Records == 0 {
		return nil, fmt.Errorf("no autopilot records found in log")
	}

	summary.FirstCycleAt = started
	summary.LastCycleAt = finished
	if !started.IsZero() && finished.After(started) {
		summary.ObservationHours = finished.Sub(started).Hours()
	}
	if summary.Cycles > 0 {
		summary.CycleHitRatePct = float64(summary.CyclesWithOpportunities) / float64(summary.Cycles) * 100
	}
	if summary.ObservationHours > 0 {
		summary.OpportunitiesPerHour = float64(summary.OpportunitiesSeen) / summary.ObservationHours
		summary.ProfitPerHourUSD = summary.LockedProfitUSD / summary.ObservationHours
	}
	switch {
	case summary.ObservationHours >= minProjectionHours:
		summary.ProjectedDailyProfitUSD = summary.ProfitPerHourUSD * 24
		if summary.DeployedUSD > 0 {
			summary.ProjectedDailyReturnPct = summary.ProjectedDailyProfitUSD / summary.DeployedUSD * 100
		}
	case summary.ObservationHours > 0:
		summary.ProjectionWarning = fmt.Sprintf("observation window %.2fh is shorter than %.0fh; daily projection suppressed because a short window cannot estimate a daily rate", summary.ObservationHours, minProjectionHours)
	}
	summary.DistinctEvents = len(events)
	summary.ByCategory = buildCategoryStats(categories, summary.OpportunitiesSeen)
	if len(summary.ByCategory) > 0 {
		summary.TopCategory = summary.ByCategory[0].Key
	}
	summary.ByHourUTC = buildHourStats(hours)

	if len(edges) > 0 {
		sort.Float64s(edges)
		sum := 0.0
		for _, edge := range edges {
			sum += edge
		}
		summary.BestNetEdgeUSD = edges[len(edges)-1]
		summary.MedianNetEdgeUSD = medianFloat64(edges)
		summary.MeanNetEdgeUSD = sum / float64(len(edges))
	}

	for reason := range halts {
		summary.HaltReasons = append(summary.HaltReasons, reason)
	}
	sort.Strings(summary.HaltReasons)

	summary.ObservationHours = roundTo(summary.ObservationHours, 3)
	summary.LongestCycleMinutes = roundTo(summary.LongestCycleMinutes, 1)
	summary.CycleHitRatePct = roundTo(summary.CycleHitRatePct, 2)
	summary.OpportunitiesPerHour = roundTo(summary.OpportunitiesPerHour, 3)
	summary.AvailableNotionalUSD = roundTo(summary.AvailableNotionalUSD, 2)
	summary.BestNetEdgeUSD = roundTo(summary.BestNetEdgeUSD, 4)
	summary.MedianNetEdgeUSD = roundTo(summary.MedianNetEdgeUSD, 4)
	summary.MeanNetEdgeUSD = roundTo(summary.MeanNetEdgeUSD, 4)
	summary.DeployedUSD = roundTo(summary.DeployedUSD, 4)
	summary.FeesPaidUSD = roundTo(summary.FeesPaidUSD, 4)
	summary.LockedProfitUSD = roundTo(summary.LockedProfitUSD, 4)
	summary.NakedLegsUSD = roundTo(summary.NakedLegsUSD, 4)
	summary.ProfitPerHourUSD = roundTo(summary.ProfitPerHourUSD, 4)
	summary.ProjectedDailyProfitUSD = roundTo(summary.ProjectedDailyProfitUSD, 4)
	summary.ProjectedDailyReturnPct = roundTo(summary.ProjectedDailyReturnPct, 2)

	summary.Verdict, summary.Note = autopilotVerdict(summary)
	return summary, nil
}

func categoryKey(value string) string {
	key := strings.TrimSpace(value)
	if key == "" {
		key = "uncategorized"
	}
	return key
}

func buildCategoryStats(accumulators map[string]*autopilotCategoryAcc, totalOpportunities int) []AutopilotCategoryStat {
	stats := make([]AutopilotCategoryStat, 0, len(accumulators))
	for key, acc := range accumulators {
		stat := AutopilotCategoryStat{
			Key:                  key,
			Opportunities:        acc.opportunities,
			DistinctEvents:       len(acc.events),
			AvailableNotionalUSD: roundTo(acc.notional, 2),
			BasketsFilled:        acc.baskets,
			LockedProfitUSD:      roundTo(acc.locked, 4),
		}
		if totalOpportunities > 0 {
			stat.OpportunitySharePct = roundTo(float64(acc.opportunities)/float64(totalOpportunities)*100, 2)
		}
		if len(acc.edges) > 0 {
			sorted := append([]float64(nil), acc.edges...)
			sort.Float64s(sorted)
			stat.BestNetEdgeUSD = roundTo(sorted[len(sorted)-1], 4)
			stat.MedianNetEdgeUSD = roundTo(medianFloat64(sorted), 4)
		}
		stats = append(stats, stat)
	}
	sort.SliceStable(stats, func(i, j int) bool {
		if stats[i].Opportunities != stats[j].Opportunities {
			return stats[i].Opportunities > stats[j].Opportunities
		}
		return stats[i].Key < stats[j].Key
	})
	return stats
}

func buildHourStats(accumulators map[int]*autopilotHourAcc) []AutopilotHourStat {
	stats := make([]AutopilotHourStat, 0, len(accumulators))
	for hour, acc := range accumulators {
		stats = append(stats, AutopilotHourStat{
			HourUTC:                 hour,
			Cycles:                  acc.cycles,
			CyclesWithOpportunities: acc.cyclesWithOpportunities,
			Opportunities:           acc.opportunities,
			BasketsFilled:           acc.baskets,
			LockedProfitUSD:         roundTo(acc.locked, 4),
			BestNetEdgeUSD:          roundTo(acc.best, 4),
		})
	}
	sort.SliceStable(stats, func(i, j int) bool { return stats[i].HourUTC < stats[j].HourUTC })
	return stats
}

func isAutopilotRecordType(recordType string) bool {
	switch recordType {
	case "autopilot_cycle", "autopilot_basket", "autopilot_abort", "autopilot_scan_error", "autopilot_halt":
		return true
	default:
		return false
	}
}

func medianFloat64(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func roundTo(value float64, places int) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	scale := math.Pow(10, float64(places))
	return math.Round(value*scale) / scale
}

func autopilotVerdict(summary *AutopilotLogSummary) (string, string) {
	const caveat = "paper simulation: displayed size is assumed executable, locked profit is not cash until settlement, and projections only extrapolate the observed window"
	switch {
	case summary.Cycles == 0:
		return "NO_DATA", "the log has no completed cycles; run at least one scan with -autopilot"
	case summary.OpportunitiesSeen == 0:
		return "NO_OPPORTUNITY", "structural arbitrage never cleared the edge floor in this window; do not trade this market with this strategy. " + caveat
	case summary.BasketsFilled == 0:
		return "NO_EXECUTION", "opportunities existed but no basket completed; inspect aborts for depth, partial fills, cooldown or budget caps. " + caveat
	case summary.LockedProfitUSD <= 0:
		return "NO_EDGE_AFTER_COSTS", "filled baskets did not lock positive edge after fees. " + caveat
	default:
		return "POSITIVE_IN_SAMPLE", "filled baskets locked positive edge in this window; treat it as an in-sample measurement, not a forecast. " + caveat
	}
}
