package polymarket

import (
	"context"
	"fmt"
	"sort"
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
	FeeBps          int     `json:"fee_bps"`
}

type ArbitrageOpportunity struct {
	EventID      string         `json:"event_id"`
	EventSlug    string         `json:"event_slug"`
	Title        string         `json:"title"`
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
			FeeBps:          feeBps,
		}
		noLeg := ArbitrageLeg{
			MarketID:        market.ID,
			MarketTitle:     title,
			Outcome:         "NO",
			TokenID:         noToken,
			Ask:             noAsk,
			AvailableShares: noSize,
			FeeBps:          feeBps,
		}
		yesLegs = append(yesLegs, yesLeg)
		noLegs = append(noLegs, noLeg)

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
		fee += leg.Ask * float64(leg.FeeBps) / 10000
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
