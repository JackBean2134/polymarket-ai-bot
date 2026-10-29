package polymarket

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func marshalLogRecord(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal log record: %v", err)
	}
	return string(data)
}

func buildSampleLog(t *testing.T) string {
	t.Helper()
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	cycle1 := AutopilotCycle{
		Index:         1,
		StartedAt:     start,
		FinishedAt:    start.Add(time.Minute),
		EventsScanned: 5,
	}
	cycle2 := AutopilotCycle{
		Index:         2,
		StartedAt:     start.Add(5 * time.Minute),
		FinishedAt:    start.Add(6 * time.Minute),
		EventsScanned: 5,
		Opportunities: 1,
		Candidates: []AutopilotCandidate{{
			EventID:      "e1",
			Kind:         "BINARY_YES_NO",
			CostUSD:      0.97,
			NetEdgeUSD:   0.03,
			MaxBaskets:   100,
			NetProfitUSD: 3,
		}},
	}
	basket := AutopilotBasket{
		EventID:         "e1",
		Kind:            "BINARY_YES_NO",
		Shares:          25,
		PayoutUSD:       25,
		CostUSD:         24.25,
		LockedProfitUSD: 0.75,
	}
	abort := AutopilotAbort{
		EventID:          "e2",
		Kind:             "BINARY_YES_NO",
		Reason:           "leg_partial_fill",
		NakedNotionalUSD: 10,
	}

	lines := []string{
		marshalLogRecord(t, DecisionRecord{Type: "autopilot_cycle", State: cycle1}),
		marshalLogRecord(t, DecisionRecord{Type: "autopilot_cycle", State: cycle2}),
		marshalLogRecord(t, DecisionRecord{Type: "autopilot_basket", State: basket}),
		marshalLogRecord(t, DecisionRecord{Type: "autopilot_abort", State: abort}),
		marshalLogRecord(t, DecisionRecord{Type: "autopilot_scan_error", Reason: "gamma unavailable"}),
		marshalLogRecord(t, DecisionRecord{Type: "autopilot_halt", Reason: "max_cycles"}),
		`{"type":"autopilot_cycle","state":`,
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestSummarizeAutopilotLog(t *testing.T) {
	summary, err := SummarizeAutopilotLog(strings.NewReader(buildSampleLog(t)))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if summary.Records != 6 || summary.SkippedLines != 1 {
		t.Fatalf("unexpected record counts: %+v", summary)
	}
	if summary.Cycles != 2 || summary.EventsScanned != 10 || summary.OpportunitiesSeen != 1 {
		t.Fatalf("unexpected cycle aggregates: %+v", summary)
	}
	if summary.CyclesWithOpportunities != 1 || summary.DistinctEvents != 1 {
		t.Fatalf("unexpected opportunity counts: %+v", summary)
	}
	if math.Abs(summary.CycleHitRatePct-50) > 1e-9 {
		t.Fatalf("expected 50%% hit rate, got %f", summary.CycleHitRatePct)
	}
	if math.Abs(summary.ObservationHours-0.1) > 1e-9 {
		t.Fatalf("expected 0.1 observation hours, got %f", summary.ObservationHours)
	}
	if math.Abs(summary.OpportunitiesPerHour-10) > 1e-6 {
		t.Fatalf("expected 10 opportunities/hour, got %f", summary.OpportunitiesPerHour)
	}
	if math.Abs(summary.BestNetEdgeUSD-0.03) > 1e-9 || math.Abs(summary.MedianNetEdgeUSD-0.03) > 1e-9 || math.Abs(summary.MeanNetEdgeUSD-0.03) > 1e-9 {
		t.Fatalf("unexpected edge stats: %+v", summary)
	}
	if math.Abs(summary.AvailableNotionalUSD-97) > 1e-6 {
		t.Fatalf("expected $97 available notional, got %f", summary.AvailableNotionalUSD)
	}
	if summary.BasketsFilled != 1 || summary.BasketsAborted != 1 || summary.ScansFailed != 1 {
		t.Fatalf("unexpected basket counts: %+v", summary)
	}
	if math.Abs(summary.DeployedUSD-24.25) > 1e-9 || math.Abs(summary.LockedProfitUSD-0.75) > 1e-9 || math.Abs(summary.NakedLegsUSD-10) > 1e-9 {
		t.Fatalf("unexpected money fields: %+v", summary)
	}
	if math.Abs(summary.ProfitPerHourUSD-7.5) > 1e-6 {
		t.Fatalf("unexpected profit per hour: %+v", summary)
	}
	// A 0.1h window must never be daily-ised.
	if summary.ProjectedDailyProfitUSD != 0 || summary.ProjectedDailyReturnPct != 0 {
		t.Fatalf("short window must suppress daily projection: %+v", summary)
	}
	if summary.ProjectionWarning == "" {
		t.Fatal("expected a projection warning for a short window")
	}
	if len(summary.HaltReasons) != 1 || summary.HaltReasons[0] != "max_cycles" {
		t.Fatalf("unexpected halt reasons: %v", summary.HaltReasons)
	}
	if summary.Verdict != "POSITIVE_IN_SAMPLE" {
		t.Fatalf("unexpected verdict %q", summary.Verdict)
	}
	if summary.Note == "" {
		t.Fatal("expected an explanatory note")
	}
}

func TestSummarizeAutopilotLogNoOpportunities(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	log := marshalLogRecord(t, DecisionRecord{
		Type: "autopilot_cycle",
		State: AutopilotCycle{
			Index:         1,
			StartedAt:     start,
			FinishedAt:    start.Add(time.Minute),
			EventsScanned: 5,
		},
	}) + "\n"

	summary, err := SummarizeAutopilotLog(strings.NewReader(log))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if summary.Verdict != "NO_OPPORTUNITY" {
		t.Fatalf("unexpected verdict %q", summary.Verdict)
	}
	if summary.ProjectedDailyProfitUSD != 0 || summary.LockedProfitUSD != 0 {
		t.Fatalf("no opportunity must not project profit: %+v", summary)
	}
	if summary.OpportunitiesPerHour != 0 {
		t.Fatalf("expected zero rate, got %f", summary.OpportunitiesPerHour)
	}
}

func TestSummarizeAutopilotLogRejectsEmptyInput(t *testing.T) {
	if _, err := SummarizeAutopilotLog(strings.NewReader("")); err == nil {
		t.Fatal("expected an error for an empty log")
	}
	if _, err := SummarizeAutopilotLog(nil); err == nil {
		t.Fatal("expected an error for a nil reader")
	}

	var onlyBad bytes.Buffer
	onlyBad.WriteString("not json\n")
	onlyBad.WriteString("{}\n")
	onlyBad.WriteString(`{"type":"autopilot_cycle","state":"not-an-object"}` + "\n")
	if _, err := SummarizeAutopilotLog(&onlyBad); err == nil {
		t.Fatal("expected an error when no records can be parsed")
	}
}

func TestSummarizeAutopilotLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "autopilot.jsonl")
	if err := os.WriteFile(path, []byte(buildSampleLog(t)), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	summary, err := SummarizeAutopilotLogFile(path)
	if err != nil {
		t.Fatalf("summarize file: %v", err)
	}
	if summary.BasketsFilled != 1 || summary.Cycles != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	if _, err := SummarizeAutopilotLogFile(filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestSummarizeAutopilotLogProjectsOnlyOverLongWindows(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cycle := AutopilotCycle{
		Index:         1,
		StartedAt:     start,
		FinishedAt:    start.Add(48 * time.Hour),
		EventsScanned: 10,
		Opportunities: 1,
	}
	basket := AutopilotBasket{
		EventID:         "e1",
		Kind:            "BINARY_YES_NO",
		CostUSD:         24.25,
		LockedProfitUSD: 0.75,
	}

	var buffer bytes.Buffer
	buffer.WriteString(marshalLogRecord(t, DecisionRecord{Type: "autopilot_cycle", State: cycle}) + "\n")
	buffer.WriteString(marshalLogRecord(t, DecisionRecord{Type: "autopilot_basket", State: basket}) + "\n")

	summary, err := SummarizeAutopilotLog(&buffer)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if math.Abs(summary.ObservationHours-48) > 1e-9 {
		t.Fatalf("expected 48h window, got %f", summary.ObservationHours)
	}
	if math.Abs(summary.ProfitPerHourUSD-0.0156) > 1e-6 {
		t.Fatalf("unexpected hourly profit: %f", summary.ProfitPerHourUSD)
	}
	if math.Abs(summary.ProjectedDailyProfitUSD-0.375) > 1e-6 {
		t.Fatalf("expected $0.375 daily projection, got %f", summary.ProjectedDailyProfitUSD)
	}
	if math.Abs(summary.ProjectedDailyReturnPct-1.55) > 0.01 {
		t.Fatalf("expected 1.55%% daily return on deployed cash, got %f", summary.ProjectedDailyReturnPct)
	}
	if summary.ProjectionWarning != "" {
		t.Fatalf("long window should not warn, got %q", summary.ProjectionWarning)
	}
}

func TestSummarizeAutopilotLogGroupsByCategoryAndHour(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	cycleA := AutopilotCycle{
		Index:         1,
		StartedAt:     start,
		FinishedAt:    start.Add(time.Minute),
		EventsScanned: 5,
		Opportunities: 2,
		Candidates: []AutopilotCandidate{
			{EventID: "s1", Category: "Sports", Kind: "BINARY_YES_NO", CostUSD: 0.95, NetEdgeUSD: 0.02, MaxBaskets: 100},
			{EventID: "s2", Category: "Sports", Kind: "BINARY_YES_NO", CostUSD: 0.95, NetEdgeUSD: 0.04, MaxBaskets: 100},
		},
	}
	cycleB := AutopilotCycle{
		Index:         2,
		StartedAt:     start.Add(time.Hour),
		FinishedAt:    start.Add(time.Hour + time.Minute),
		EventsScanned: 5,
		Opportunities: 1,
		Candidates: []AutopilotCandidate{
			{EventID: "m1", Category: "Macro", Kind: "NEGRISK_YES", CostUSD: 0.95, NetEdgeUSD: 0.03, MaxBaskets: 100},
		},
	}
	basket := AutopilotBasket{
		EventID:         "s1",
		Category:        "Sports",
		Kind:            "BINARY_YES_NO",
		CostUSD:         23.75,
		LockedProfitUSD: 0.5,
		FilledAt:        start.Add(5 * time.Minute),
	}

	var buffer bytes.Buffer
	for _, record := range []DecisionRecord{
		{Type: "autopilot_cycle", State: cycleA},
		{Type: "autopilot_cycle", State: cycleB},
		{Type: "autopilot_basket", State: basket},
	} {
		buffer.WriteString(marshalLogRecord(t, record) + "\n")
	}

	summary, err := SummarizeAutopilotLog(&buffer)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if summary.TopCategory != "Sports" {
		t.Fatalf("expected Sports as top category, got %q", summary.TopCategory)
	}
	if len(summary.ByCategory) != 2 {
		t.Fatalf("expected 2 categories, got %+v", summary.ByCategory)
	}

	sports := summary.ByCategory[0]
	if sports.Key != "Sports" || sports.Opportunities != 2 || sports.DistinctEvents != 2 {
		t.Fatalf("unexpected sports stats: %+v", sports)
	}
	if math.Abs(sports.OpportunitySharePct-66.67) > 0.01 {
		t.Fatalf("expected 66.67%% share, got %f", sports.OpportunitySharePct)
	}
	if math.Abs(sports.BestNetEdgeUSD-0.04) > 1e-9 || math.Abs(sports.MedianNetEdgeUSD-0.03) > 1e-9 {
		t.Fatalf("unexpected sports edge stats: %+v", sports)
	}
	if math.Abs(sports.AvailableNotionalUSD-190) > 1e-6 {
		t.Fatalf("expected $190 available notional, got %f", sports.AvailableNotionalUSD)
	}
	if sports.BasketsFilled != 1 || math.Abs(sports.LockedProfitUSD-0.5) > 1e-9 {
		t.Fatalf("unexpected sports execution stats: %+v", sports)
	}

	macro := summary.ByCategory[1]
	if macro.Key != "Macro" || macro.Opportunities != 1 || macro.BasketsFilled != 0 {
		t.Fatalf("unexpected macro stats: %+v", macro)
	}
	if math.Abs(macro.OpportunitySharePct-33.33) > 0.01 {
		t.Fatalf("expected 33.33%% share, got %f", macro.OpportunitySharePct)
	}

	if len(summary.ByHourUTC) != 2 {
		t.Fatalf("expected 2 hourly buckets, got %+v", summary.ByHourUTC)
	}
	first := summary.ByHourUTC[0]
	if first.HourUTC != 10 || first.Cycles != 1 || first.Opportunities != 2 || first.BasketsFilled != 1 {
		t.Fatalf("unexpected hour 10 stats: %+v", first)
	}
	if math.Abs(first.LockedProfitUSD-0.5) > 1e-9 || math.Abs(first.BestNetEdgeUSD-0.04) > 1e-9 {
		t.Fatalf("unexpected hour 10 money stats: %+v", first)
	}
	second := summary.ByHourUTC[1]
	if second.HourUTC != 11 || second.Cycles != 1 || second.Opportunities != 1 || second.BasketsFilled != 0 {
		t.Fatalf("unexpected hour 11 stats: %+v", second)
	}
}

func TestSummarizeAutopilotLogFallsBackToUncategorized(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	cycle := AutopilotCycle{
		Index:         1,
		StartedAt:     start,
		FinishedAt:    start.Add(time.Minute),
		EventsScanned: 3,
		Opportunities: 1,
		Candidates: []AutopilotCandidate{
			{EventID: "x1", Kind: "BINARY_YES_NO", CostUSD: 0.97, NetEdgeUSD: 0.02, MaxBaskets: 10},
		},
	}
	var buffer bytes.Buffer
	buffer.WriteString(marshalLogRecord(t, DecisionRecord{Type: "autopilot_cycle", State: cycle}) + "\n")

	summary, err := SummarizeAutopilotLog(&buffer)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(summary.ByCategory) != 1 || summary.ByCategory[0].Key != "uncategorized" {
		t.Fatalf("expected a single uncategorized bucket, got %+v", summary.ByCategory)
	}
	if summary.TopCategory != "uncategorized" {
		t.Fatalf("unexpected top category %q", summary.TopCategory)
	}
}

func TestSummarizeAutopilotLogFlagsStalledCycles(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	cycle := AutopilotCycle{
		Index:         1,
		StartedAt:     start,
		FinishedAt:    start.Add(41 * time.Minute),
		EventsScanned: 20,
	}

	var buffer bytes.Buffer
	buffer.WriteString(marshalLogRecord(t, DecisionRecord{Type: "autopilot_cycle", State: cycle}) + "\n")

	summary, err := SummarizeAutopilotLog(&buffer)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if math.Abs(summary.LongestCycleMinutes-41) > 0.05 {
		t.Fatalf("expected a 41 minute longest cycle, got %f", summary.LongestCycleMinutes)
	}
	if len(summary.DataQualityWarnings) != 1 {
		t.Fatalf("expected one data-quality warning, got %v", summary.DataQualityWarnings)
	}
	if !strings.Contains(summary.DataQualityWarnings[0], "41 minutes") {
		t.Fatalf("unexpected warning: %q", summary.DataQualityWarnings[0])
	}
}

func TestMedianFloat64HandlesEvenAndEmpty(t *testing.T) {
	if got := medianFloat64([]float64{1, 2, 3, 4}); got != 2.5 {
		t.Fatalf("expected 2.5, got %f", got)
	}
	if got := medianFloat64([]float64{5}); got != 5 {
		t.Fatalf("expected 5, got %f", got)
	}
	if got := medianFloat64(nil); got != 0 {
		t.Fatalf("expected 0, got %f", got)
	}
}
