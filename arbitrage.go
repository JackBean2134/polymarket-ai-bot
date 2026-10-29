package polymarket

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type ArbitrageLeg struct {
	MarketID        string  `json:"market_id"`
	MarketTitle     string  `json:"market_title"`
	Outcome         string  `json:"outcome"`
	TokenID         string  `json:"token_id"`
	Ask             float64 `json:"ask"`
	AvailableShares float64 `json:"available_shares"`
	// FeeRate/FeeExponent come from the market's feeSchedule; FeeBps is the
	// legacy field and is only a fallback for hand-built legs.
	FeeRate     float64 `json:"fee_rate,omitempty"`
	FeeExponent float64 `json:"fee_exponent,omitempty"`
	FeeBps      int     `json:"fee_bps"`
}

type ArbitrageOpportunity struct {
	EventID      string         `json:"event_id"`
	EventSlug    string         `json:"event_slug"`
	Title        string         `json:"title"`
	Category     string         `json:"category,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	Kind         string         `json:"kind"`
	Legs         []ArbitrageLeg `json:"legs"`
	CostUSD      float64        `json:"cost_usd"`
	PayoutUSD    float64        `json:"payout_usd"`
	GrossEdgeUSD float64        `json:"gross_edge_usd"`
	FeeUSD       float64        `json:"fee_usd"`
	NetEdgeUSD   float64        `json:"net_edge_usd"`
	MaxBaskets   float64        `json:"max_baskets"`
	NetProfitUSD float64        `json:"net_profit_usd"`
	Timestamp    time.Time      `json:"timestamp"`
}

type ArbitrageScanStats struct {
	EventsScanned     int            `json:"events_scanned"`
	EventsWithNegRisk int            `json:"events_with_neg_risk"`
	MarketsScanned    int            `json:"markets_scanned"`
	BooksFetched      int            `json:"books_fetched"`
	Opportunities     int            `json:"opportunities"`
	Errors            int            `json:"errors"`
	SkipReasons       map[string]int `json:"skip_reasons,omitempty"`
	// BestBinary* describe the cheapest YES+NO basket seen, even when it was
	// not profitable. They answer "how far from an arbitrage were we?".
	BestBinaryCostUSD    float64 `json:"best_binary_cost_usd"`
	BestBinaryFeeUSD     float64 `json:"best_binary_fee_usd"`
	BestBinaryNetEdgeUSD float64 `json:"best_binary_net_edge_usd"`
}

type ArbitrageScanResult struct {
	StartedAt     time.Time              `json:"started_at"`
	FinishedAt    time.Time              `json:"finished_at"`
	MinEdgeUSD    float64                `json:"min_edge_usd"`
	Stats         ArbitrageScanStats     `json:"stats"`
	Opportunities []ArbitrageOpportunity `json:"opportunities"`
}

type ArbitrageScanner struct {
	clob        *CLOBService
	gamma       *GammaService
	concurrency int
	maxMarkets  int
}

func NewArbitrageScanner(client *Client) *ArbitrageScanner {
	return &ArbitrageScanner{
		clob:        NewCLOBService(client),
		gamma:       NewGammaService(client),
		concurrency: 4,
		maxMarkets:  40,
	}
}

func (s *ArbitrageScanner) SetConcurrency(value int) {
	if value > 0 {
		s.concurrency = value
	}
}

func (s *ArbitrageScanner) SetMaxMarketsPerEvent(value int) {
	if value > 0 {
		s.maxMarkets = value
	}
}

func (s *ArbitrageScanner) ScanEvents(ctx context.Context, limit int, minEdgeUSD float64) (*ArbitrageScanResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if minEdgeUSD < 0 {
		minEdgeUSD = 0
	}

	started := time.Now().UTC()
	active := true
	closed := false
	ascending := false
	resp, err := s.gamma.ListEvents(ctx, &ListEventsParams{
		Active:    &active,
		Closed:    &closed,
		Limit:     limit,
		Order:     "volume24hr",
		Ascending: &ascending,
	})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	workers := s.concurrency
	if workers > len(resp.Events) {
		workers = len(resp.Events)
	}
	if workers < 1 {
		workers = 1
	}

	type result struct {
		stats ArbitrageScanStats
		opps  []ArbitrageOpportunity
	}
	jobs := make(chan Event)
	results := make(chan result, len(resp.Events))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for event := range jobs {
				stats, opps := s.scanEvent(ctx, event, minEdgeUSD)
				results <- result{stats: stats, opps: opps}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, event := range resp.Events {
			select {
			case <-ctx.Done():
				return
			case jobs <- event:
			}
		}
	}()

	wg.Wait()
	close(results)

	scan := &ArbitrageScanResult{
		StartedAt:     started,
		FinishedAt:    time.Now().UTC(),
		MinEdgeUSD:    minEdgeUSD,
		Opportunities: make([]ArbitrageOpportunity, 0),
	}
	for item := range results {
		scan.Stats.EventsScanned += item.stats.EventsScanned
		scan.Stats.EventsWithNegRisk += item.stats.EventsWithNegRisk
		scan.Stats.MarketsScanned += item.stats.MarketsScanned
		scan.Stats.BooksFetched += item.stats.BooksFetched
		scan.Stats.Errors += item.stats.Errors
		if item.stats.BestBinaryCostUSD > 0 && (scan.Stats.BestBinaryCostUSD == 0 || item.stats.BestBinaryCostUSD < scan.Stats.BestBinaryCostUSD) {
			scan.Stats.BestBinaryCostUSD = item.stats.BestBinaryCostUSD
			scan.Stats.BestBinaryFeeUSD = item.stats.BestBinaryFeeUSD
			scan.Stats.BestBinaryNetEdgeUSD = item.stats.BestBinaryNetEdgeUSD
		}
		for reason, count := range item.stats.SkipReasons {
			if scan.Stats.SkipReasons == nil {
				scan.Stats.SkipReasons = make(map[string]int)
			}
			scan.Stats.SkipReasons[reason] += count
		}
		scan.Opportunities = append(scan.Opportunities, item.opps...)
	}
	scan.Stats.Opportunities = len(scan.Opportunities)
	sort.SliceStable(scan.Opportunities, func(i, j int) bool {
		return scan.Opportunities[i].NetProfitUSD > scan.Opportunities[j].NetProfitUSD
	})
	return scan, nil
}

func (s *ArbitrageScanner) scanEvent(ctx context.Context, event Event, minEdgeUSD float64) (ArbitrageScanStats, []ArbitrageOpportunity) {
	stats := ArbitrageScanStats{EventsScanned: 1, SkipReasons: make(map[string]int)}
	skip := func(reason string) {
		stats.SkipReasons[reason]++
	}
	if event.NegRisk {
		stats.EventsWithNegRisk = 1
	}

	var opportunities []ArbitrageOpportunity
	if opportunities == nil {
		opportunities = make([]ArbitrageOpportunity, 0)
	}
	markets := make([]Market, 0, len(event.Markets))
	for _, market := range event.Markets {
		if market.Closed || !market.AcceptingOrders {
			skip("closed_or_not_accepting")
			continue
		}
		markets = append(markets, market)
	}
	complete := len(markets) >= 2 && len(markets) == len(event.Markets)
	if len(markets) > s.maxMarkets {
		markets = markets[:s.maxMarkets]
		complete = false
		skip("event_markets_capped")
	}
	yesLegs := make([]ArbitrageLeg, 0, len(markets))
	noLegs := make([]ArbitrageLeg, 0, len(markets))

	for _, market := range markets {
		select {
		case <-ctx.Done():
			stats.Errors++
			return stats, opportunities
		default:
		}
		yesToken, noToken, err := market.BinaryTokenIDs()
		if err != nil {
			complete = false
			skip("invalid_token_ids")
			continue
		}

		yesBook, yesErr := s.clob.GetOrderBook(ctx, yesToken)
		noBook, noErr := s.clob.GetOrderBook(ctx, noToken)
		if yesErr != nil || noErr != nil {
			stats.Errors++
			complete = false
			skip("book_error")
			continue
		}
		stats.MarketsScanned++
		stats.BooksFetched += 2

		yesAsk, yesSize, okYes := bestAsk(yesBook)
		noAsk, noSize, okNo := bestAsk(noBook)
		if !okYes || !okNo {
			complete = false
			skip("missing_ask")
			continue
		}

		feeBps := market.TakerBaseFee
		if feeBps < 0 {
			feeBps = 0
		}
		// The real taker fee comes from the market's feeSchedule; takerBaseFee
		// is a legacy bps field that must not be applied to notional.
		category := eventCategory(event)
		feeRate := MarketFeeRate(market, category)
		feeExponent := MarketFeeExponent(market)
		title := market.Title
		if title == "" {
			title = market.Question
		}

		yesLeg := ArbitrageLeg{
			MarketID:        market.ID,
			MarketTitle:     title,
			Outcome:         "YES",
			TokenID:         yesToken,
			Ask:             yesAsk,
			AvailableShares: yesSize,
			FeeRate:         feeRate,
			FeeExponent:     feeExponent,
			FeeBps:          feeBps,
		}
		noLeg := ArbitrageLeg{
			MarketID:        market.ID,
			MarketTitle:     title,
			Outcome:         "NO",
			TokenID:         noToken,
			Ask:             noAsk,
			AvailableShares: noSize,
			FeeRate:         feeRate,
			FeeExponent:     feeExponent,
			FeeBps:          feeBps,
		}
		yesLegs = append(yesLegs, yesLeg)
		noLegs = append(noLegs, noLeg)

		basketCost := yesAsk + noAsk
		basketFee := takerFeeForLeg(yesLeg, yesAsk, 1) + takerFeeForLeg(noLeg, noAsk, 1)
		if stats.BestBinaryCostUSD == 0 || basketCost < stats.BestBinaryCostUSD {
			stats.BestBinaryCostUSD = basketCost
			stats.BestBinaryFeeUSD = basketFee
			stats.BestBinaryNetEdgeUSD = 1 - basketCost - basketFee
		}

		if opp, ok := evaluateBasket(event, "BINARY_YES_NO", []ArbitrageLeg{yesLeg, noLeg}, 1, minEdgeUSD); ok {
			opportunities = append(opportunities, opp)
		}
	}

	if complete && event.NegRisk && len(yesLegs) >= 2 && len(noLegs) == len(yesLegs) {
		if opp, ok := evaluateBasket(event, "NEGRISK_YES", yesLegs, 1, minEdgeUSD); ok {
			opportunities = append(opportunities, opp)
		}
		noPayout := float64(len(noLegs) - 1)
		if noPayout > 0 {
			if opp, ok := evaluateBasket(event, "NEGRISK_NO", noLegs, noPayout, minEdgeUSD); ok {
				opportunities = append(opportunities, opp)
			}
		}
	}

	return stats, opportunities
}

func evaluateBasket(event Event, kind string, legs []ArbitrageLeg, payout float64, minEdgeUSD float64) (ArbitrageOpportunity, bool) {
	if len(legs) == 0 || payout <= 0 {
		return ArbitrageOpportunity{}, false
	}
	cost := 0.0
	fee := 0.0
	maxBaskets := 0.0
	for i, leg := range legs {
		if leg.Ask <= 0 || leg.Ask >= 1 || leg.AvailableShares <= 0 {
			return ArbitrageOpportunity{}, false
		}
		cost += leg.Ask
		fee += takerFeeForLeg(leg, leg.Ask, 1)
		if i == 0 || leg.AvailableShares < maxBaskets {
			maxBaskets = leg.AvailableShares
		}
	}
	gross := payout - cost
	net := gross - fee
	if net < minEdgeUSD || maxBaskets < 1 {
		return ArbitrageOpportunity{}, false
	}
	return ArbitrageOpportunity{
		EventID:      event.ID,
		EventSlug:    event.Slug,
		Title:        event.Title,
		Category:     eventCategory(event),
		Tags:         eventTagLabels(event),
		Kind:         kind,
		Legs:         legs,
		CostUSD:      cost,
		PayoutUSD:    payout,
		GrossEdgeUSD: gross,
		FeeUSD:       fee,
		NetEdgeUSD:   net,
		MaxBaskets:   maxBaskets,
		NetProfitUSD: net * maxBaskets,
		Timestamp:    time.Now().UTC(),
	}, true
}

func bestAsk(book *OrderBook) (price, size float64, ok bool) {
	if book == nil {
		return 0, 0, false
	}
	for _, level := range book.Asks {
		if level.Price <= 0 || level.Price >= 1 || level.Size <= 0 {
			continue
		}
		if !ok || level.Price < price {
			price = level.Price
			size = level.Size
			ok = true
		}
	}
	return price, size, ok
}

// takerFeeForLeg charges the protocol fee when a schedule rate is present and
// only falls back to the legacy bps-on-notional model otherwise.
func takerFeeForLeg(leg ArbitrageLeg, price, shares float64) float64 {
	if leg.FeeRate > 0 {
		return TakerFeeUSD(price, shares, leg.FeeRate, leg.FeeExponent)
	}
	if leg.FeeBps > 0 {
		return price * shares * float64(leg.FeeBps) / 10000
	}
	return 0
}

// canonicalEventCategories is a fixed priority list. Polymarket's events list
// exposes tags but often no category, and tags are ordered by specificity
// ("Esports|Games|Sports"). Without a canonical bucket, grouping would shatter
// into dozens of near-duplicate labels.
var canonicalEventCategories = []struct {
	Label string
	Slug  string
}{
	{"Sports", "sports"},
	{"Politics", "politics"},
	{"Elections", "elections"},
	{"Crypto", "crypto"},
	{"Economy", "economy"},
	{"Geopolitics", "geopolitics"},
	{"World", "world"},
	{"Culture", "culture"},
	{"Tech", "tech"},
	{"Business", "business"},
	{"Science", "science"},
	{"Health", "health"},
	{"Climate", "climate"},
	{"Weather", "weather"},
}

// eventCategory resolves a grouping label for reporting: Gamma's category
// when present, otherwise the highest-priority canonical tag, otherwise the
// first tag, otherwise "uncategorized".
func eventCategory(event Event) string {
	if category := strings.TrimSpace(event.Category); category != "" {
		return category
	}
	labels := eventTagLabels(event)
	if len(labels) == 0 {
		return "uncategorized"
	}
	for _, canonical := range canonicalEventCategories {
		for _, label := range labels {
			if strings.EqualFold(label, canonical.Label) || strings.EqualFold(label, canonical.Slug) {
				return canonical.Label
			}
		}
	}
	return labels[0]
}

// eventTagLabels returns deduplicated, non-empty tag labels (or slugs).
func eventTagLabels(event Event) []string {
	if len(event.Tags) == 0 {
		return nil
	}
	labels := make([]string, 0, len(event.Tags))
	seen := make(map[string]struct{}, len(event.Tags))
	for _, tag := range event.Tags {
		label := strings.TrimSpace(tag.Label)
		if label == "" {
			label = strings.TrimSpace(tag.Name)
		}
		if label == "" {
			label = strings.TrimSpace(tag.Slug)
		}
		if label == "" {
			continue
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		labels = append(labels, label)
	}
	return labels
}
