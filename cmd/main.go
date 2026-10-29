package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/ChainOpera-Network/nofx/polymarket"
)

type cliOptions struct {
	configPath        string
	backtestPath      string
	calibratePath     string
	autopilotReport   string
	secretsAudit      bool
	secretsRoot       string
	calibrationBins   int
	collectResolved   int
	collectPageSize   int
	historyFidelity   int
	collectEndDateMin string
	collectEndDateMax string
	collectPause      time.Duration
	collectTimeout    time.Duration
	buildSamples      bool
	dataDir           string
	reportDir         string
	sampleOffsets     string
	sampleStaleness   time.Duration
	momentumWeight    float64
	sampleHoldout     float64
	sampleBuckets     int
	showStatus        bool
	showMetrics       bool
	showPositions     bool
	pauseTrading      bool
	resumeTrading     bool
	listMarkets       int
	minPrice          float64
	maxPrice          float64
	minDays           int
	marketID          string
	wallet            string
	arbScan           int
	arbMinEdge        float64
	arbOut            string
	arbInterval       int
	arbConcurrency    int
	arbMaxMarkets     int
	autopilotCycles   int
	autopilotInterval int
	autopilotScan     int
	autopilotMinEdge  float64
	autopilotMaxUSD   float64
	autopilotBudget   float64
	autopilotEventUSD float64
	autopilotUnhedged float64
	autopilotCooldown int
	autopilotOut      string
	liveToken         string
	liveVerify        string
	polygonRPC        string
	liveSide          string
	liveShares        float64
	livePrice         float64
	liveMaxUSD        float64
	liveOrderType     string
	liveDryRun        bool
	liveConfirm       string
	liveCancel        string
	backtestOptions   polymarket.BacktestOptions
}

func main() {
	configPathDefault := os.Getenv("POLYMARKET_CONFIG")
	if configPathDefault == "" {
		configPathDefault = "polymarket_config.json"
	}

	options := cliOptions{}
	flag.StringVar(&options.configPath, "config", configPathDefault, "Path to configuration file")
	flag.StringVar(&options.backtestPath, "backtest", "", "Run a CSV backtest without connecting to Polymarket")
	flag.StringVar(&options.calibratePath, "calibrate", "", "Compute Brier score, log loss and reliability buckets from a resolved CSV")
	flag.IntVar(&options.calibrationBins, "calibration-bins", 10, "Number of reliability buckets for -calibrate")
	flag.IntVar(&options.collectResolved, "collect-resolved", 0, "Collect N settled binary markets with historical prices into the data directory")
	flag.IntVar(&options.collectPageSize, "collect-page-size", 100, "Gamma page size for -collect-resolved")
	flag.IntVar(&options.historyFidelity, "history-fidelity", 60, "Price-history fidelity in minutes")
	flag.StringVar(&options.collectEndDateMin, "collect-end-date-min", "", "Only collect closed markets ending on/after this date (default: last 30 days)")
	flag.StringVar(&options.collectEndDateMax, "collect-end-date-max", "", "Only collect closed markets ending on/before this date (default: today)")
	flag.DurationVar(&options.collectPause, "collect-pause", 100*time.Millisecond, "Pause between history requests")
	flag.DurationVar(&options.collectTimeout, "collect-timeout", 30*time.Minute, "Overall timeout for -collect-resolved")
	flag.BoolVar(&options.buildSamples, "build-samples", false, "Build timestamped research samples and calibration reports from collected data")
	flag.StringVar(&options.dataDir, "data-dir", "data", "Directory for collected markets and price history")
	flag.StringVar(&options.reportDir, "report-dir", "reports", "Directory for sample CSVs and calibration reports")
	flag.StringVar(&options.sampleOffsets, "sample-offsets", "168h,48h,24h,6h", "Comma-separated offsets before close for -build-samples")
	flag.DurationVar(&options.sampleStaleness, "sample-max-staleness", 6*time.Hour, "Maximum age of the last price point used for a sample")
	flag.Float64Var(&options.momentumWeight, "momentum-weight", 0.5, "Weight for ROC_logit in the transparent logit-momentum baseline")
	flag.Float64Var(&options.sampleHoldout, "sample-holdout", 0.3, "Fraction of latest samples reserved for chronological holdout")
	flag.IntVar(&options.sampleBuckets, "sample-buckets", 10, "Reliability bucket count for -build-samples")
	flag.Float64Var(&options.backtestOptions.StartingCapital, "capital", 10000, "Starting backtest capital in USD")
	flag.Float64Var(&options.backtestOptions.StakeUSD, "stake", 100, "Fixed stake per backtest trade in USD")
	flag.Float64Var(&options.backtestOptions.MinEdge, "min-edge", 0.05, "Minimum absolute model edge as a probability (0.05 = 5 percentage points)")
	flag.Float64Var(&options.backtestOptions.FeeBps, "fee-bps", -1, "Required approximate fee in basis points of stake (0 only if justified)")
	flag.Float64Var(&options.backtestOptions.SlippageBps, "slippage-bps", -1, "Required adverse entry-price adjustment in basis points")
	flag.BoolVar(&options.showStatus, "status", false, "Show loaded configuration status")
	flag.BoolVar(&options.showMetrics, "metrics", false, "Show trading metrics")
	flag.BoolVar(&options.showPositions, "positions", false, "Show open positions")
	flag.BoolVar(&options.pauseTrading, "pause", false, "Pause trading")
	flag.BoolVar(&options.resumeTrading, "resume", false, "Resume trading")
	flag.IntVar(&options.listMarkets, "markets", 0, "List N active binary markets with liquidity filters and exit")
	flag.Float64Var(&options.minPrice, "min-price", 0.02, "Exclude markets with YES price below this (decimal)")
	flag.Float64Var(&options.maxPrice, "max-price", 0.98, "Exclude markets with YES price above this (decimal)")
	flag.IntVar(&options.minDays, "min-days", 0, "Exclude markets expiring in fewer than N days")
	flag.StringVar(&options.marketID, "market", "", "Print a read-only order-book snapshot for a Gamma market ID")
	flag.StringVar(&options.wallet, "wallet", "", "Print read-only positions and recent trades for an address")
	flag.IntVar(&options.arbScan, "arb-scan", 0, "Scan N top events for YES/NO and negRisk basket arbitrage (read-only)")
	flag.Float64Var(&options.arbMinEdge, "arb-min-edge", 0.005, "Minimum net edge per $1 basket payout for -arb-scan")
	flag.StringVar(&options.arbOut, "arb-out", "", "Append each -arb-scan result as JSONL to this file")
	flag.IntVar(&options.arbInterval, "arb-interval", 0, "Repeat -arb-scan every N seconds until interrupted (0 = once)")
	flag.IntVar(&options.arbConcurrency, "arb-concurrency", 4, "Concurrent event scans for -arb-scan")
	flag.IntVar(&options.arbMaxMarkets, "arb-max-markets", 40, "Maximum markets to fetch per event (baskets require the full event)")
	flag.IntVar(&options.autopilotCycles, "autopilot", 0, "Run N supervised paper-only arbitrage cycles (0 = disabled)")
	flag.IntVar(&options.autopilotInterval, "autopilot-interval", 120, "Seconds between autopilot cycles")
	flag.IntVar(&options.autopilotScan, "autopilot-scan", 20, "Events scanned per autopilot cycle")
	flag.Float64Var(&options.autopilotMinEdge, "autopilot-min-edge", 0.02, "Minimum net edge per $1 basket payout")
	flag.Float64Var(&options.autopilotMaxUSD, "autopilot-max-usd", 25, "Maximum cash per basket")
	flag.Float64Var(&options.autopilotBudget, "autopilot-budget", 100, "Total cash budget for the autopilot run")
	flag.Float64Var(&options.autopilotEventUSD, "autopilot-max-event-usd", 25, "Maximum cash per event")
	flag.Float64Var(&options.autopilotUnhedged, "autopilot-max-unhedged", 25, "Halt once unhedged leg notional reaches this amount")
	flag.IntVar(&options.autopilotCooldown, "autopilot-cooldown", 300, "Seconds before the same event can be traded again")
	flag.StringVar(&options.autopilotOut, "autopilot-out", "", "Append autopilot decisions as JSONL to this file")
	flag.StringVar(&options.autopilotReport, "autopilot-report", "", "Summarise an -autopilot-out JSONL file offline")
	flag.BoolVar(&options.secretsAudit, "secrets-audit", false, "Scan the working tree for committed credentials (offline, never prints values)")
	flag.StringVar(&options.secretsRoot, "secrets-root", ".", "Directory to scan for -secrets-audit")
	flag.StringVar(&options.liveToken, "live-order", "", "Token ID for a live order (requires explicit confirmation)")
	flag.StringVar(&options.liveVerify, "live-verify", "", "Run read-only pre-flight checks for a token (never sends an order)")
	flag.StringVar(&options.polygonRPC, "polygon-rpc", os.Getenv("POLYMARKET_POLYGON_RPC"), "Polygon JSON-RPC endpoint for -live-verify allowance checks")
	flag.StringVar(&options.liveSide, "live-side", "BUY", "Live order side: BUY or SELL")
	flag.Float64Var(&options.liveShares, "live-shares", 0, "Live order size in conditional token shares")
	flag.Float64Var(&options.livePrice, "live-price", 0, "Live order limit price (decimal, e.g. 0.10)")
	flag.Float64Var(&options.liveMaxUSD, "live-max-usd", 0, "Hard maximum USD cost for this live order (max 25)")
	flag.StringVar(&options.liveOrderType, "live-order-type", "FAK", "Live order type: FAK, FOK or GTC")
	flag.BoolVar(&options.liveDryRun, "live-dry-run", true, "Sign and print the live order without sending it")
	flag.StringVar(&options.liveConfirm, "live-confirm", "", "Set to I_UNDERSTAND to actually send a live order")
	flag.StringVar(&options.liveCancel, "live-cancel", "", "Cancel a live order by order ID (requires L2 credentials)")
	flag.Parse()

	if err := run(options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(options cliOptions) error {
	if options.secretsAudit {
		audit, err := polymarket.AuditSecrets(options.secretsRoot)
		if err != nil {
			return err
		}
		return prettyPrint(audit)
	}

	if options.backtestPath != "" {
		file, err := os.Open(options.backtestPath)
		if err != nil {
			return fmt.Errorf("open backtest CSV: %w", err)
		}
		defer file.Close()

		result, err := polymarket.BacktestCSV(file, options.backtestOptions)
		if err != nil {
			return err
		}
		return prettyPrint(result)
	}

	if options.calibratePath != "" {
		file, err := os.Open(options.calibratePath)
		if err != nil {
			return fmt.Errorf("open calibration CSV: %w", err)
		}
		defer file.Close()

		report, err := polymarket.CalibrationCSV(file, options.calibrationBins)
		if err != nil {
			return err
		}
		return prettyPrint(report)
	}

	if options.autopilotReport != "" {
		summary, err := polymarket.SummarizeAutopilotLogFile(options.autopilotReport)
		if err != nil {
			return err
		}
		return prettyPrint(summary)
	}

	if options.buildSamples {
		offsets, err := parseSampleOffsets(options.sampleOffsets)
		if err != nil {
			return err
		}
		summary, err := polymarket.BuildSamples(polymarket.SampleBuildOptions{
			DataDir:         options.dataDir,
			ReportDir:       options.reportDir,
			Offsets:         offsets,
			MaxStaleness:    options.sampleStaleness,
			MomentumWeight:  options.momentumWeight,
			HoldoutFraction: options.sampleHoldout,
			Buckets:         options.sampleBuckets,
		})
		if err != nil {
			return err
		}
		return prettyPrint(summary)
	}

	selectedCommands := 0
	for _, selected := range []bool{
		options.showStatus, options.showMetrics, options.showPositions,
		options.pauseTrading, options.resumeTrading,
		options.listMarkets > 0, options.marketID != "", options.wallet != "",
		options.arbScan > 0, options.autopilotCycles > 0, options.liveToken != "", options.liveVerify != "", options.liveCancel != "",
		options.collectResolved > 0,
	} {
		if selected {
			selectedCommands++
		}
	}
	if selectedCommands > 1 {
		return errors.New("choose only one status, metrics, positions, pause, resume, markets, market, wallet, arb-scan, autopilot, live-order, live-verify, live-cancel, or collect-resolved command")
	}

	config, err := loadConfig(options.configPath)
	if err != nil {
		return err
	}

	switch {
	case options.showStatus:
		return prettyPrint(getStatus(config))
	case options.showMetrics:
		return errors.New("no persistent trade ledger is implemented; run -backtest with a resolved-signal CSV")
	case options.showPositions:
		return errors.New("live position tracking is not implemented")
	case options.pauseTrading || options.resumeTrading:
		return errors.New("there is no running trading process or persistent pause state to control")
	case options.listMarkets > 0:
		return runMarkets(config, options.listMarkets, options.minPrice, options.maxPrice, options.minDays)
	case options.marketID != "":
		return runMarketSnapshot(config, options.marketID)
	case options.wallet != "":
		return runWallet(config, options.wallet)
	case options.arbScan > 0:
		return runArbScan(config, options)
	case options.autopilotCycles > 0:
		return runAutopilot(config, options)
	case options.liveToken != "":
		return runLiveOrder(config, options)
	case options.liveVerify != "":
		return runLiveVerify(config, options)
	case options.liveCancel != "":
		return runLiveCancel(config, options.liveCancel)
	case options.collectResolved > 0:
		return runCollectResolved(config, options)
	default:
		return runBot(config)
	}
}

func loadConfig(path string) (*polymarket.Config, error) {
	config, err := polymarket.LoadConfigFrom(path)
	if err == nil {
		return config, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "warning: %s not found; using safe defaults (copy polymarket_config.example.json to polymarket_config.json to customise)\n", path)
		return polymarket.DefaultConfigFromEnv(), nil
	}
	return nil, err
}

func newClient(config *polymarket.Config) *polymarket.Client {
	return polymarket.NewClient(polymarket.ClientConfig{
		APIKey:        config.Polymarket.APIKey,
		APISecret:     config.Polymarket.APISecret,
		Passphrase:    config.Polymarket.Passphrase,
		WalletAddress: config.Polymarket.WalletAddress,
		Timeout:       config.GetTimeout(),
		RetryCount:    config.GetRetryCount(),
		RetryWait:     config.GetRetryWait(),
	})
}

type marketSummary struct {
	MarketID   string    `json:"market_id"`
	Slug       string    `json:"slug"`
	Title      string    `json:"title"`
	YesPrice   float64   `json:"yes_price"`
	NoPrice    float64   `json:"no_price"`
	Volume     float64   `json:"volume"`
	Volume24h  float64   `json:"volume_24h"`
	Liquidity  float64   `json:"liquidity"`
	EndDate    time.Time `json:"end_date"`
	YesTokenID string    `json:"yes_token_id"`
	NoTokenID  string    `json:"no_token_id"`
	Accepting  bool      `json:"accepting_orders"`
	URL        string    `json:"url"`
}

func summariseMarket(market polymarket.Market) marketSummary {
	yesToken, noToken, _ := market.BinaryTokenIDs()
	return marketSummary{
		MarketID:   market.ID,
		Slug:       market.Slug,
		Title:      market.Title,
		YesPrice:   market.YesPrice,
		NoPrice:    market.NoPrice,
		Volume:     market.Volume,
		Volume24h:  market.Volume24h,
		Liquidity:  market.Liquidity,
		EndDate:    market.ExpirationTime,
		YesTokenID: yesToken,
		NoTokenID:  noToken,
		Accepting:  market.AcceptingOrders,
		URL:        "https://polymarket.com/market/" + market.Slug,
	}
}

func runMarkets(config *polymarket.Config, limit int, minPrice, maxPrice float64, minDays int) error {
	ctx, cancel := context.WithTimeout(context.Background(), config.GetTimeout()+10*time.Second)
	defer cancel()

	if minPrice < 0 || maxPrice > 1 || minPrice >= maxPrice {
		return fmt.Errorf("require 0 <= min-price < max-price <= 1")
	}

	fetchLimit := limit * 20
	if fetchLimit < 100 {
		fetchLimit = 100
	}
	if fetchLimit > 500 {
		fetchLimit = 500
	}

	active := true
	closed := false
	ascending := false
	gamma := polymarket.NewGammaService(newClient(config))
	resp, err := gamma.ListMarkets(ctx, &polymarket.ListMarketsParams{
		Active:       &active,
		Closed:       &closed,
		Limit:        fetchLimit,
		Order:        "volume24hr",
		Ascending:    &ascending,
		LiquidityMin: config.Trading.MinLiquidity,
	})
	if err != nil {
		return fmt.Errorf("list markets: %w", err)
	}

	earliestExpiry := time.Now().Add(time.Duration(minDays) * 24 * time.Hour)
	summaries := make([]marketSummary, 0, len(resp.Markets))
	for _, market := range resp.Markets {
		if len(market.ClobTokenIDs) < 2 || len(market.Outcomes) != 2 {
			continue
		}
		if market.YesPrice < minPrice || market.YesPrice > maxPrice {
			continue
		}
		if minDays > 0 && (market.ExpirationTime.IsZero() || market.ExpirationTime.Before(earliestExpiry)) {
			continue
		}
		summaries = append(summaries, summariseMarket(market))
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].Volume24h > summaries[j].Volume24h
	})
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	if len(summaries) == 0 {
		return fmt.Errorf("no active binary markets matched price band [%.2f, %.2f] and min-days %d", minPrice, maxPrice, minDays)
	}
	return prettyPrint(summaries)
}

func runMarketSnapshot(config *polymarket.Config, marketID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), config.GetTimeout()+10*time.Second)
	defer cancel()

	client := newClient(config)
	gamma := polymarket.NewGammaService(client)
	market, err := gamma.GetMarketByID(ctx, marketID)
	if err != nil {
		return fmt.Errorf("get market: %w", err)
	}
	yesToken, noToken, err := market.BinaryTokenIDs()
	if err != nil {
		return err
	}

	clob := polymarket.NewCLOBService(client)
	book, bookErr := clob.GetOrderBook(ctx, yesToken)
	price, priceErr := clob.GetMidpointPrice(ctx, yesToken)
	if bookErr != nil && priceErr != nil {
		return fmt.Errorf("fetch CLOB data: orderbook=%v price=%v", bookErr, priceErr)
	}

	snapshot := map[string]interface{}{
		"market": summariseMarket(*market),
	}
	if bookErr == nil {
		bestBid, bestAsk := 0.0, 0.0
		if len(book.Bids) > 0 {
			bestBid = book.Bids[0].Price
		}
		if len(book.Asks) > 0 {
			bestAsk = book.Asks[0].Price
		}
		snapshot["orderbook"] = map[string]interface{}{
			"best_bid":     bestBid,
			"best_ask":     bestAsk,
			"spread":       bestAsk - bestBid,
			"bid_levels":   len(book.Bids),
			"ask_levels":   len(book.Asks),
			"condition_id": book.ConditionID,
			"yes_token_id": yesToken,
			"no_token_id":  noToken,
			"timestamp":    book.Timestamp,
		}
	}
	if priceErr == nil {
		if bookErr == nil {
			if len(book.Bids) > 0 {
				price.BidPrice = book.Bids[0].Price
			}
			if len(book.Asks) > 0 {
				price.AskPrice = book.Asks[0].Price
			}
		}
		snapshot["midpoint"] = price
	}

	// Logit-space features and the article's momentum filter. These are
	// research signals, not a calibrated probability or a trade instruction.
	if history, err := clob.GetPriceHistory(ctx, &polymarket.GetPriceHistoryParams{
		MarketID: yesToken,
		Interval: "1w",
		Fidelity: 60,
		Limit:    500,
	}); err == nil {
		prices := make([]float64, 0, len(history.Prices))
		for _, point := range history.Prices {
			prices = append(prices, point.Price)
		}
		priceFeatures := polymarket.ComputePriceFeatures(prices, 6, 14, 50, 25)
		bookFeatures := polymarket.ComputeOrderBookFeatures(book, 10)
		timeToExpiry := time.Until(market.ExpirationTime)
		if market.ExpirationTime.IsZero() {
			timeToExpiry = 0
		}
		decision := polymarket.EvaluateMomentumEntry(priceFeatures, bookFeatures, 100, timeToExpiry, polymarket.DefaultMomentumRules())
		snapshot["features"] = map[string]interface{}{
			"price":                priceFeatures,
			"book":                 bookFeatures,
			"time_to_expiry_hours": timeToExpiry.Hours(),
		}
		snapshot["momentum_decision"] = decision
	} else {
		snapshot["features_error"] = err.Error()
	}
	return prettyPrint(snapshot)
}

func runWallet(config *polymarket.Config, address string) error {
	address = strings.TrimSpace(address)
	if !strings.HasPrefix(address, "0x") || len(address) != 42 {
		return fmt.Errorf("wallet must be a 42-character 0x address")
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.GetTimeout()+10*time.Second)
	defer cancel()

	data := polymarket.NewDataService(newClient(config))
	positions, err := data.GetUserPositions(ctx, &polymarket.GetUserPositionsParams{UserID: address, Limit: 100})
	if err != nil {
		return fmt.Errorf("get positions: %w", err)
	}
	trades, err := data.GetTrades(ctx, &polymarket.GetTradesParams{UserID: address, Limit: 100})
	if err != nil {
		return fmt.Errorf("get trades: %w", err)
	}

	totalValue := 0.0
	totalPnL := 0.0
	for _, position := range positions.Positions {
		totalValue += position.CurrentValue
		totalPnL += position.PnL
	}

	return prettyPrint(map[string]interface{}{
		"address":        address,
		"position_count": len(positions.Positions),
		"trade_count":    len(trades.Trades),
		"position_value": totalValue,
		"unrealized_pnl": totalPnL,
		"positions":      positions.Positions,
		"recent_trades":  trades.Trades,
	})
}

func runCollectResolved(config *polymarket.Config, options cliOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), options.collectTimeout)
	defer cancel()

	collector := polymarket.NewResearchCollector(newClient(config), options.dataDir)
	collector.SetEndDateMin(options.collectEndDateMin)
	collector.SetEndDateMax(options.collectEndDateMax)
	summary, err := collector.CollectResolved(ctx, options.collectResolved, options.collectPageSize, options.historyFidelity, options.collectPause)
	if summary != nil {
		_ = prettyPrint(summary)
	}
	return err
}

func parseSampleOffsets(value string) ([]time.Duration, error) {
	parts := strings.Split(value, ",")
	offsets := make([]time.Duration, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		offset, err := time.ParseDuration(part)
		if err != nil || offset <= 0 {
			return nil, fmt.Errorf("invalid sample offset %q", part)
		}
		offsets = append(offsets, offset)
	}
	if len(offsets) == 0 {
		return nil, fmt.Errorf("at least one sample offset is required")
	}
	return offsets, nil
}

func runArbScan(config *polymarket.Config, options cliOptions) error {
	scanner := polymarket.NewArbitrageScanner(newClient(config))
	scanner.SetConcurrency(options.arbConcurrency)
	scanner.SetMaxMarketsPerEvent(options.arbMaxMarkets)

	ctx := context.Background()
	if options.arbInterval > 0 {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
	}

	for {
		if err := polymarket.CheckStopFile(config.Trading.StopFile); err != nil {
			return err
		}
		scanCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		result, err := scanner.ScanEvents(scanCtx, options.arbScan, options.arbMinEdge)
		cancel()
		if err == nil {
			_ = polymarket.WriteHeartbeat(config.Trading.HeartbeatFile)
		}
		if err != nil {
			if options.arbInterval <= 0 {
				return err
			}
			fmt.Fprintf(os.Stderr, "scan error: %v\n", err)
		} else {
			if options.arbOut != "" {
				if err := appendJSONL(options.arbOut, result); err != nil {
					return err
				}
			}
			if options.arbInterval <= 0 {
				return prettyPrint(result)
			}
			best := 0.0
			if len(result.Opportunities) > 0 {
				best = result.Opportunities[0].NetProfitUSD
			}
			fmt.Printf("%s events=%d negRiskEvents=%d books=%d opps=%d bestNetProfit=%.4f bestBinaryCost=%.3f bestBinaryNet=%.4f\n",
				result.FinishedAt.Format(time.RFC3339),
				result.Stats.EventsScanned,
				result.Stats.EventsWithNegRisk,
				result.Stats.BooksFetched,
				result.Stats.Opportunities,
				best,
				result.Stats.BestBinaryCostUSD,
				result.Stats.BestBinaryNetEdgeUSD,
			)
		}

		if options.arbInterval <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(options.arbInterval) * time.Second):
		}
	}
}

func runAutopilot(config *polymarket.Config, options cliOptions) error {
	client := newClient(config)
	scanner := polymarket.NewArbitrageScanner(client)
	scanner.SetConcurrency(options.arbConcurrency)
	scanner.SetMaxMarketsPerEvent(options.arbMaxMarkets)

	// PaperExecutor walks the live book but never signs or submits an order.
	executor := polymarket.NewPaperExecutor(polymarket.NewCLOBService(client), 0)
	bot, err := polymarket.NewAutopilot(scanner, executor, polymarket.AutopilotConfig{
		ScanEvents:             options.autopilotScan,
		Interval:               time.Duration(options.autopilotInterval) * time.Second,
		CycleTimeout:           120 * time.Second,
		MinNetEdgeUSD:          options.autopilotMinEdge,
		MaxPerBasketUSD:        options.autopilotMaxUSD,
		MaxTotalUSD:            options.autopilotBudget,
		MaxPerEventUSD:         options.autopilotEventUSD,
		MaxUnhedgedUSD:         options.autopilotUnhedged,
		MaxCycles:              options.autopilotCycles,
		MaxConsecutiveFailures: 10,
		EventCooldown:          time.Duration(options.autopilotCooldown) * time.Second,
		StopFile:               config.Trading.StopFile,
		HeartbeatFile:          config.Trading.HeartbeatFile,
		DecisionLog:            options.autopilotOut,
	})
	if err != nil {
		return err
	}
	defer bot.Close()

	fmt.Fprintln(os.Stderr, "autopilot is PAPER ONLY: it reads public books, simulates fills and never sends an order")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	report := bot.Run(ctx)
	return prettyPrint(report)
}

func appendJSONL(path string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode jsonl: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func runLiveVerify(config *polymarket.Config, options cliOptions) error {
	token := strings.TrimSpace(options.liveVerify)
	if token == "" {
		return fmt.Errorf("-live-verify requires a token ID")
	}
	shares := options.liveShares
	if shares <= 0 {
		shares = 5
	}
	maxUSD := options.liveMaxUSD
	if maxUSD <= 0 {
		maxUSD = 5
	}
	const hardCapUSD = 25.0
	if maxUSD > hardCapUSD {
		return fmt.Errorf("live-max-usd must not exceed the hard cap of %.0f", hardCapUSD)
	}
	if options.livePrice < 0 || options.livePrice >= 1 {
		return fmt.Errorf("live-price must be a decimal in (0,1) or 0 to use the best quote")
	}
	side := strings.ToUpper(strings.TrimSpace(options.liveSide))
	if side == "" {
		side = "BUY"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client := newClient(config)
	var signer *polymarket.OrderSigner
	if strings.TrimSpace(config.Polymarket.PrivateKey) != "" {
		s, err := polymarket.NewOrderSigner(polymarket.OrderSignerConfig{
			PrivateKeyHex: config.Polymarket.PrivateKey,
			ChainID:       config.Polymarket.ChainID,
			FunderAddress: config.Polymarket.FunderAddress,
			SignatureType: config.Polymarket.SignatureType,
		})
		if err != nil {
			return err
		}
		signer = s
	}

	envLive := strings.EqualFold(strings.TrimSpace(os.Getenv("POLYMARKET_ALLOW_LIVE")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("POLYMARKET_ALLOW_LIVE")), "true")

	rpcURL := strings.TrimSpace(options.polygonRPC)
	if rpcURL == "" {
		rpcURL = strings.TrimSpace(os.Getenv("POLYGON_RPC_URL"))
	}
	rpc := polymarket.NewRPCClient(rpcURL)

	report, err := polymarket.VerifyLiveOrder(ctx, polymarket.LiveVerifyDeps{
		Config:  config,
		Client:  client,
		CLOB:    polymarket.NewCLOBService(client),
		Gamma:   polymarket.NewGammaService(client),
		Signer:  signer,
		EnvLive: envLive,
		Allowances: func(ctx context.Context, chainID int64, owner, exchange string, requiredUSD float64) (*polymarket.AllowanceResult, error) {
			return polymarket.CheckTradingAllowances(ctx, rpc, chainID, owner, exchange, requiredUSD)
		},
		Positions: func(ctx context.Context, chainID int64, owner, tokenID string) (float64, error) {
			return polymarket.CheckCTFBalance(ctx, rpc, chainID, owner, tokenID)
		},
	}, polymarket.OrderRequest{
		TokenID:    token,
		Side:       side,
		Shares:     shares,
		LimitPrice: options.livePrice,
		MaxCashUSD: maxUSD,
	})
	if err != nil {
		return err
	}
	return prettyPrint(report)
}

func runLiveOrder(config *polymarket.Config, options cliOptions) error {
	side := strings.ToUpper(strings.TrimSpace(options.liveSide))
	if side != "BUY" && side != "SELL" {
		return fmt.Errorf("live-side must be BUY or SELL")
	}
	if options.liveShares < 5 {
		return fmt.Errorf("live-shares must be at least 5 (Polymarket minimum order size)")
	}
	if options.livePrice <= 0 || options.livePrice >= 1 {
		return fmt.Errorf("live-price must be a decimal in (0,1)")
	}
	if options.liveMaxUSD <= 0 {
		return fmt.Errorf("live-max-usd is required")
	}
	const hardCapUSD = 25.0
	if options.liveMaxUSD > hardCapUSD {
		return fmt.Errorf("live-max-usd must not exceed the hard cap of %.0f", hardCapUSD)
	}
	if config.Risk.MaxPositionSize > 0 && options.liveMaxUSD > config.Risk.MaxPositionSize {
		return fmt.Errorf("live-max-usd must not exceed risk.max_position_size %.2f", config.Risk.MaxPositionSize)
	}
	notional := options.liveShares * options.livePrice
	if notional > options.liveMaxUSD+1e-9 {
		return fmt.Errorf("order notional %.2f exceeds live-max-usd %.2f", notional, options.liveMaxUSD)
	}
	if strings.TrimSpace(config.Polymarket.PrivateKey) == "" {
		return fmt.Errorf("POLYMARKET_PRIVATE_KEY is required to sign the order")
	}

	signer, err := polymarket.NewOrderSigner(polymarket.OrderSignerConfig{
		PrivateKeyHex: config.Polymarket.PrivateKey,
		ChainID:       config.Polymarket.ChainID,
		FunderAddress: config.Polymarket.FunderAddress,
		SignatureType: config.Polymarket.SignatureType,
	})
	if err != nil {
		return err
	}
	if config.Polymarket.WalletAddress != "" && !strings.EqualFold(config.Polymarket.WalletAddress, signer.Address()) {
		return fmt.Errorf("POLYMARKET_WALLET_ADDRESS %s does not match signer %s", config.Polymarket.WalletAddress, signer.Address())
	}
	if !options.liveDryRun {
		if err := config.LiveOrderAllowed(options.liveToken); err != nil {
			return err
		}
		if options.liveConfirm != "I_UNDERSTAND" {
			return fmt.Errorf("set -live-confirm=I_UNDERSTAND to actually send the order")
		}
		client := newClient(config)
		if !client.HasL2Credentials() {
			return fmt.Errorf("POLYMARKET_API_KEY, API_SECRET, PASSPHRASE and WALLET_ADDRESS are required")
		}
		if config.Polymarket.WalletAddress == "" {
			return fmt.Errorf("POLYMARKET_WALLET_ADDRESS is required for L2 authentication")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client := newClient(config)
	clob := polymarket.NewCLOBService(client)
	tick, err := clob.GetTickSize(ctx, options.liveToken)
	if err != nil {
		return fmt.Errorf("get tick size: %w", err)
	}
	tickSize := tick.TickSize
	if tickSize <= 0 {
		tickSize = 0.001
	}
	negRisk, err := clob.GetNegRisk(ctx, options.liveToken)
	if err != nil {
		return fmt.Errorf("get neg-risk flag: %w", err)
	}
	feeBps, err := clob.GetFeeRateBps(ctx, options.liveToken)
	if err != nil {
		return fmt.Errorf("get fee rate: %w", err)
	}

	// takerBaseFee above is the legacy signing value, not the fee rate. Resolve
	// the real schedule so the preview shows what the protocol will charge.
	gamma := polymarket.NewGammaService(client)
	market, marketErr := gamma.GetMarketByTokenID(ctx, options.liveToken)
	feeInfo := map[string]interface{}{"source": "unavailable"}
	estimatedFee := 0.0
	estimatedTotal := notional
	switch {
	case marketErr != nil:
		feeInfo["error"] = marketErr.Error()
	case market == nil || !market.FeeRateKnown:
		feeInfo["error"] = "market does not expose a feeSchedule"
	default:
		exponent := polymarket.MarketFeeExponent(*market)
		feeInfo = map[string]interface{}{
			"source":                "gamma.feeSchedule",
			"rate":                  market.FeeRate,
			"exponent":              exponent,
			"fees_enabled":          market.FeesEnabled,
			"legacy_taker_base_fee": market.TakerBaseFee,
		}
		estimatedFee = polymarket.TakerFeeUSD(options.livePrice, options.liveShares, market.FeeRate, exponent)
		estimatedTotal = notional + estimatedFee
	}
	if estimatedTotal > options.liveMaxUSD+1e-9 {
		return fmt.Errorf("order cost including the protocol fee %.2f exceeds live-max-usd %.2f", estimatedTotal, options.liveMaxUSD)
	}

	order, err := signer.BuildSignedOrder(options.liveToken, side, options.liveShares, options.livePrice, tickSize, feeBps, negRisk)
	if err != nil {
		return err
	}

	preview := map[string]interface{}{
		"dry_run":            options.liveDryRun,
		"token_id":           options.liveToken,
		"side":               side,
		"shares":             options.liveShares,
		"limit_price":        options.livePrice,
		"estimated_notional": notional,
		"max_usd":            options.liveMaxUSD,
		"tick_size":          tickSize,
		"fee_rate_bps":       feeBps,
		"taker_fee":          feeInfo,
		"estimated_fee":      estimatedFee,
		"estimated_total":    estimatedTotal,
		"neg_risk":           negRisk,
		"signer":             signer.Address(),
		"funder":             signer.Funder(),
		"order_hash":         order.OrderHash,
		"order":              order,
	}

	if options.liveDryRun {
		return prettyPrint(preview)
	}

	fmt.Fprintln(os.Stderr, "sending LIVE order in 2 seconds; press Ctrl+C to abort")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}

	executor, err := polymarket.NewLiveExecutor(clob, signer, options.liveOrderType, options.liveMaxUSD)
	if err != nil {
		return err
	}
	executor.SetStopFile(config.Trading.StopFile)
	executor.SetAllowedTokens(config.Trading.AllowedTokens)
	executor.SetFeeResolver(polymarket.NewGammaService(client))
	fill, err := executor.Execute(ctx, polymarket.OrderRequest{
		TokenID:    options.liveToken,
		Side:       side,
		Shares:     options.liveShares,
		LimitPrice: options.livePrice,
		MaxCashUSD: options.liveMaxUSD,
		OrderType:  options.liveOrderType,
		NegRisk:    negRisk,
	})
	if err != nil {
		return err
	}
	return prettyPrint(map[string]interface{}{
		"fill":        fill,
		"token_id":    options.liveToken,
		"side":        side,
		"shares":      options.liveShares,
		"limit_price": options.livePrice,
		"max_usd":     options.liveMaxUSD,
		"note":        "the executor signs a fresh order with a new salt; the dry-run preview is not the sent order",
	})
}

func runLiveCancel(config *polymarket.Config, orderID string) error {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return fmt.Errorf("live-cancel requires an order ID")
	}
	client := newClient(config)
	if !client.HasL2Credentials() {
		return fmt.Errorf("POLYMARKET_API_KEY, API_SECRET, PASSPHRASE and WALLET_ADDRESS are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	clob := polymarket.NewCLOBService(client)
	if err := clob.CancelOrder(ctx, orderID); err != nil {
		return err
	}
	return prettyPrint(map[string]interface{}{"canceled": orderID})
}

func getStatus(config *polymarket.Config) map[string]interface{} {
	return map[string]interface{}{
		"running":                      false,
		"trading_mode":                 config.Trading.Mode,
		"trading_enabled":              config.Trading.Enabled,
		"total_capital":                config.Risk.TotalCapital,
		"strategies":                   config.Trading.AllowedStrategies,
		"polymarket_credentials_set":   config.Polymarket.APIKey != "",
		"polymarket_wallet_configured": config.Polymarket.WalletAddress != "",
		"polymarket_signer_configured": config.Polymarket.PrivateKey != "",
		"ai_credentials_set":           config.AI.APIKey != "",
		"live_ordering_implemented":    true,
		"live_ordering_requires_flags": true,
		"live_paper_switch":            config.Trading.Mode == "paper" || !config.Trading.Enabled,
		"live_allow_live_config":       config.Trading.AllowLive,
		"live_allow_live_env":          strings.ToLower(strings.TrimSpace(os.Getenv("POLYMARKET_ALLOW_LIVE"))) == "1" || strings.EqualFold(strings.TrimSpace(os.Getenv("POLYMARKET_ALLOW_LIVE")), "true"),
		"live_whitelist_count":         len(config.Trading.AllowedTokens),
		"kill_switch_path":             config.Trading.StopFile,
		"kill_switch_active":           polymarket.CheckStopFile(config.Trading.StopFile) != nil,
		"heartbeat_file":               config.Trading.HeartbeatFile,
	}
}

func runBot(config *polymarket.Config) error {
	if config.Trading.Mode == "live" || config.Trading.Enabled {
		return errors.New("the automatic live trading loop is deliberately disabled; use the explicit -live-order command with the documented guards")
	}
	return errors.New("online scanning/trading as a service is not implemented; use -backtest, -markets/-market/-wallet/-arb-scan for research, or -live-order for a gated single order")
}

func prettyPrint(value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	_, err = io.WriteString(os.Stdout, string(data)+"\n")
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}
