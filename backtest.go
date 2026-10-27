package polymarket

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type BacktestOptions struct {
	StartingCapital float64
	StakeUSD        float64
	MinEdge         float64
	FeeBps          float64
	SlippageBps     float64
}

type BacktestTrade struct {
	Timestamp  time.Time `json:"timestamp"`
	MarketID   string    `json:"market_id"`
	Outcome    string    `json:"outcome"`
	EntryPrice float64   `json:"entry_price"`
	StakeUSD   float64   `json:"stake_usd"`
	FeeUSD     float64   `json:"fee_usd"`
	NetPnL     float64   `json:"net_pnl"`
	Capital    float64   `json:"capital_after"`
}

type BacktestResult struct {
	Signals           int             `json:"signals"`
	Trades            int             `json:"trades"`
	SkippedForEdge    int             `json:"skipped_for_edge"`
	SkippedForCapital int             `json:"skipped_for_capital"`
	SkippedForPrice   int             `json:"skipped_for_price"`
	ProfitableTrades  int             `json:"profitable_trades"`
	WinRate           float64         `json:"win_rate"`
	FeesUSD           float64         `json:"fees_usd"`
	NetPnL            float64         `json:"net_pnl"`
	ReturnPercent     float64         `json:"return_percent"`
	ProfitFactor      *float64        `json:"profit_factor"`
	MaxDrawdown       float64         `json:"max_drawdown"`
	FinalCapital      float64         `json:"final_capital"`
	TradeLog          []BacktestTrade `json:"trade_log"`
}

type backtestSignal struct {
	timestamp time.Time
	marketID  string
	yesPrice  float64
	modelProb float64
	resolved  bool
}

func BacktestCSV(reader io.Reader, options BacktestOptions) (*BacktestResult, error) {
	if !isFinite(options.StartingCapital) || !isFinite(options.StakeUSD) || options.StartingCapital <= 0 || options.StakeUSD <= 0 {
		return nil, fmt.Errorf("starting capital and stake must be positive")
	}
	if !isFinite(options.MinEdge) || options.MinEdge <= 0 || options.MinEdge >= 1 {
		return nil, fmt.Errorf("minimum edge must be in (0, 1)")
	}
	if !isFinite(options.FeeBps) || options.FeeBps < 0 || options.FeeBps >= 10000 {
		return nil, fmt.Errorf("fee basis points must be in [0, 10000)")
	}
	if !isFinite(options.SlippageBps) || options.SlippageBps < 0 || options.SlippageBps >= 10000 {
		return nil, fmt.Errorf("slippage basis points must be in [0, 10000)")
	}

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

	var signals []backtestSignal
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
		marketID := strings.TrimSpace(record[columns["market_id"]])
		if marketID == "" {
			return nil, fmt.Errorf("CSV row %d has an empty market_id", rowNumber)
		}
		yesPrice, parseErr := parseBacktestProbability(record[columns["market_yes_price"]])
		if parseErr != nil || yesPrice <= 0 || yesPrice >= 1 {
			return nil, fmt.Errorf("CSV row %d market_yes_price must be between 0 and 1", rowNumber)
		}
		modelProb, parseErr := parseBacktestProbability(record[columns["model_yes_probability"]])
		if parseErr != nil || modelProb < 0 || modelProb > 1 {
			return nil, fmt.Errorf("CSV row %d model_yes_probability must be between 0 and 1", rowNumber)
		}
		outcome := strings.ToUpper(strings.TrimSpace(record[columns["resolved_outcome"]]))
		if outcome != "YES" && outcome != "NO" {
			return nil, fmt.Errorf("CSV row %d resolved_outcome must be YES or NO", rowNumber)
		}

		signals = append(signals, backtestSignal{
			timestamp: timestamp,
			marketID:  marketID,
			yesPrice:  yesPrice,
			modelProb: modelProb,
			resolved:  outcome == "YES",
		})
	}

	if len(signals) == 0 {
		return nil, fmt.Errorf("CSV contains no signal rows")
	}
	sort.SliceStable(signals, func(i, j int) bool {
		return signals[i].timestamp.Before(signals[j].timestamp)
	})

	result := &BacktestResult{
		Signals:      len(signals),
		FinalCapital: options.StartingCapital,
		TradeLog:     make([]BacktestTrade, 0, len(signals)),
	}
	capital := options.StartingCapital
	peakCapital := capital
	var winningPnL, losingPnL float64
	slippage := options.SlippageBps / 10000
	feeRate := options.FeeBps / 10000

	for _, signal := range signals {
		edge := signal.modelProb - signal.yesPrice
		outcome := ""
		entryPrice := 0.0
		if edge >= options.MinEdge {
			outcome = "YES"
			entryPrice = signal.yesPrice
		} else if edge <= -options.MinEdge {
			outcome = "NO"
			entryPrice = 1 - signal.yesPrice
		} else {
			result.SkippedForEdge++
			continue
		}

		entryPrice *= 1 + slippage
		if entryPrice <= 0 || entryPrice >= 1 {
			result.SkippedForPrice++
			continue
		}
		fee := options.StakeUSD * feeRate
		if options.StakeUSD+fee > capital {
			result.SkippedForCapital++
			continue
		}

		shares := options.StakeUSD / entryPrice
		won := signal.resolved == (outcome == "YES")
		payout := 0.0
		if won {
			payout = shares
		}
		netPnL := payout - options.StakeUSD - fee
		capital += netPnL

		result.Trades++
		result.FeesUSD += fee
		result.NetPnL += netPnL
		if netPnL > 0 {
			result.ProfitableTrades++
			winningPnL += netPnL
		} else if netPnL < 0 {
			losingPnL += -netPnL
		}
		if capital > peakCapital {
			peakCapital = capital
		}
		if peakCapital > 0 {
			drawdown := (peakCapital - capital) / peakCapital
			if drawdown > result.MaxDrawdown {
				result.MaxDrawdown = drawdown
			}
		}
		result.TradeLog = append(result.TradeLog, BacktestTrade{
			Timestamp:  signal.timestamp,
			MarketID:   signal.marketID,
			Outcome:    outcome,
			EntryPrice: entryPrice,
			StakeUSD:   options.StakeUSD,
			FeeUSD:     fee,
			NetPnL:     netPnL,
			Capital:    capital,
		})
	}

	result.FinalCapital = capital
	if result.Trades > 0 {
		result.WinRate = float64(result.ProfitableTrades) / float64(result.Trades) * 100
	}
	if options.StartingCapital > 0 {
		result.ReturnPercent = result.NetPnL / options.StartingCapital * 100
	}
	if losingPnL > 0 {
		profitFactor := winningPnL / losingPnL
		result.ProfitFactor = &profitFactor
	}
	return result, nil
}

func parseBacktestProbability(value string) (float64, error) {
	probability, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || !isFinite(probability) {
		return 0, fmt.Errorf("invalid probability")
	}
	return probability, nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
