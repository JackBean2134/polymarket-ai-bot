package polymarket

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CollectedMarket struct {
	MarketID            string    `json:"market_id"`
	Slug                string    `json:"slug"`
	Title               string    `json:"title"`
	Question            string    `json:"question"`
	Description         string    `json:"description,omitempty"`
	ResolutionSource    string    `json:"resolution_source,omitempty"`
	GroupItemTitle      string    `json:"group_item_title,omitempty"`
	EventID             string    `json:"event_id,omitempty"`
	Outcomes            []string  `json:"outcomes"`
	OutcomePrices       []float64 `json:"outcome_prices"`
	YesTokenID          string    `json:"yes_token_id"`
	NoTokenID           string    `json:"no_token_id"`
	ResolvedOutcome     string    `json:"resolved_outcome"`
	EndDate             time.Time `json:"end_date"`
	ClosedTime          time.Time `json:"closed_time"`
	UMAResolutionStatus string    `json:"uma_resolution_status,omitempty"`
	Volume              float64   `json:"volume"`
	Liquidity           float64   `json:"liquidity"`
	NegRisk             bool      `json:"neg_risk"`
	TakerBaseFee        int       `json:"taker_base_fee"`
	TickSize            float64   `json:"tick_size"`
}

type CollectedHistory struct {
	MarketID   string              `json:"market_id"`
	YesTokenID string              `json:"yes_token_id"`
	Points     []PriceHistoryPoint `json:"points"`
}

type CollectionSummary struct {
	Requested   int       `json:"requested"`
	Collected   int       `json:"collected"`
	Existing    int       `json:"existing"`
	Skipped     int       `json:"skipped"`
	Errors      int       `json:"errors"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	DataDir     string    `json:"data_dir"`
	EndDateMin  string    `json:"end_date_min"`
	EndDateMax  string    `json:"end_date_max"`
	SummaryJSON string    `json:"summary_json,omitempty"`
}

type ResearchCollector struct {
	gamma      *GammaService
	clob       *CLOBService
	dataDir    string
	endDateMin string
	endDateMax string
}

func NewResearchCollector(client *Client, dataDir string) *ResearchCollector {
	if dataDir == "" {
		dataDir = "data"
	}
	return &ResearchCollector{
		gamma:   NewGammaService(client),
		clob:    NewCLOBService(client),
		dataDir: dataDir,
	}
}

func (c *ResearchCollector) SetEndDateMin(value string) {
	c.endDateMin = value
}

func (c *ResearchCollector) SetEndDateMax(value string) {
	c.endDateMax = value
}

func (c *ResearchCollector) CollectResolved(ctx context.Context, target int, pageSize int, fidelity int, pause time.Duration) (*CollectionSummary, error) {
	if target <= 0 {
		return nil, fmt.Errorf("target must be positive")
	}
	if pageSize <= 0 {
		pageSize = 100
	}
	if fidelity <= 0 {
		fidelity = 60
	}
	if pause <= 0 {
		pause = 100 * time.Millisecond
	}
	if err := os.MkdirAll(c.dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	marketsPath := filepath.Join(c.dataDir, "markets.jsonl")
	historyPath := filepath.Join(c.dataDir, "price_history.jsonl")

	if c.endDateMin == "" {
		c.endDateMin = time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if c.endDateMax == "" {
		c.endDateMax = time.Now().UTC().Format("2006-01-02")
	}

	existing, err := loadCollectedMarketIDs(marketsPath)
	if err != nil {
		return nil, err
	}
	summary := &CollectionSummary{
		Requested:  target,
		Existing:   len(existing),
		Collected:  len(existing),
		StartedAt:  time.Now().UTC(),
		DataDir:    c.dataDir,
		EndDateMin: c.endDateMin,
		EndDateMax: c.endDateMax,
	}
	if len(existing) >= target {
		summary.Collected = len(existing)
		summary.FinishedAt = time.Now().UTC()
		summary.SummaryJSON = filepath.Join(c.dataDir, "collection_summary.json")
		if err := writeJSONFile(summary.SummaryJSON, summary); err != nil {
			return summary, err
		}
		return summary, nil
	}

	closed := true
	descending := false
	offset := 0
	for summary.Collected < target {
		if err := ctx.Err(); err != nil {
			summary.FinishedAt = time.Now().UTC()
			return summary, err
		}
		response, err := c.gamma.ListMarkets(ctx, &ListMarketsParams{
			Closed:     &closed,
			Limit:      pageSize,
			Offset:     offset,
			Order:      "volumeNum",
			Ascending:  &descending,
			EndDateMin: c.endDateMin,
			EndDateMax: c.endDateMax,
		})
		if err != nil {
			return summary, fmt.Errorf("list closed markets offset %d: %w", offset, err)
		}
		if len(response.Markets) == 0 {
			break
		}

		for _, market := range response.Markets {
			if summary.Collected >= target {
				break
			}
			if _, ok := existing[market.ID]; ok {
				continue
			}
			outcome, err := ResolveBinaryOutcome(&market)
			if err != nil {
				summary.Skipped++
				continue
			}
			yesToken, noToken, err := market.BinaryTokenIDs()
			if err != nil {
				summary.Skipped++
				continue
			}
			history, err := c.clob.GetPriceHistory(ctx, &GetPriceHistoryParams{
				MarketID: yesToken,
				Interval: "max",
				Fidelity: fidelity,
			})
			if (err != nil || history == nil || len(history.Prices) < 2) && fidelity > 1 {
				history, err = c.clob.GetPriceHistory(ctx, &GetPriceHistoryParams{
					MarketID: yesToken,
					Interval: "max",
					Fidelity: 1,
				})
			}
			if err != nil || history == nil || len(history.Prices) < 2 {
				summary.Errors++
				if err == nil {
					err = fmt.Errorf("history has fewer than 2 points")
				}
				fmt.Fprintf(os.Stderr, "skip market %s: %v\n", market.ID, err)
				continue
			}

			collected := CollectedMarket{
				MarketID:            market.ID,
				Slug:                market.Slug,
				Title:               market.Title,
				Question:            market.Question,
				Description:         market.Description,
				ResolutionSource:    market.ResolutionSource,
				GroupItemTitle:      market.GroupItemTitle,
				EventID:             market.EventID,
				Outcomes:            market.Outcomes,
				OutcomePrices:       market.OutcomePrices,
				YesTokenID:          yesToken,
				NoTokenID:           noToken,
				ResolvedOutcome:     outcome,
				EndDate:             market.ExpirationTime,
				ClosedTime:          market.ClosedTime,
				UMAResolutionStatus: market.UMAResolutionStatus,
				Volume:              market.Volume,
				Liquidity:           market.Liquidity,
				NegRisk:             market.NegRisk,
				TakerBaseFee:        market.TakerBaseFee,
				TickSize:            market.TickSize,
			}
			if err := appendJSONLine(historyPath, CollectedHistory{
				MarketID:   market.ID,
				YesTokenID: yesToken,
				Points:     history.Prices,
			}); err != nil {
				return summary, err
			}
			if err := appendJSONLine(marketsPath, collected); err != nil {
				return summary, err
			}
			existing[market.ID] = struct{}{}
			summary.Collected = len(existing)
			fmt.Fprintf(os.Stderr, "collected %d/%d: %s (%s)\n", summary.Collected, target, market.ID, outcome)
			if pause > 0 {
				select {
				case <-ctx.Done():
					summary.FinishedAt = time.Now().UTC()
					return summary, ctx.Err()
				case <-time.After(pause):
				}
			}
		}
		offset += len(response.Markets)
		if len(response.Markets) < pageSize {
			break
		}
	}
	summary.FinishedAt = time.Now().UTC()
	summary.SummaryJSON = filepath.Join(c.dataDir, "collection_summary.json")
	if err := writeJSONFile(summary.SummaryJSON, summary); err != nil {
		return summary, err
	}
	if summary.Collected < target {
		return summary, fmt.Errorf("only collected %d of %d settled markets with usable history", summary.Collected, target)
	}
	return summary, nil
}

func appendJSONLine(path string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
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

func loadCollectedMarketIDs(path string) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ids, nil
		}
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
		var item CollectedMarket
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		if item.MarketID != "" {
			ids[item.MarketID] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return ids, nil
}
