package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type DataService struct {
	client *Client
}

func NewDataService(client *Client) *DataService {
	return &DataService{client: client}
}

type UserPosition struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	MarketID     string    `json:"market_id"`
	MarketTitle  string    `json:"market_title"`
	Outcome      string    `json:"outcome"`
	Position     float64   `json:"position"`
	AveragePrice float64   `json:"average_price"`
	TotalCost    float64   `json:"total_cost"`
	CurrentValue float64   `json:"current_value"`
	PnL          float64   `json:"pnl"`
	PnLPercent   float64   `json:"pnl_percent"`
	RealizedPnL  float64   `json:"realized_pnl"`
	LastUpdated  time.Time `json:"last_updated"`
	IsClosed     bool      `json:"is_closed"`
	ClosedAt     time.Time `json:"closed_at,omitempty"`
}

func (p *UserPosition) UnmarshalJSON(data []byte) error {
	var raw struct {
		ProxyWallet  string          `json:"proxyWallet"`
		Asset        string          `json:"asset"`
		ConditionID  string          `json:"conditionId"`
		Size         json.RawMessage `json:"size"`
		AvgPrice     json.RawMessage `json:"avgPrice"`
		InitialValue json.RawMessage `json:"initialValue"`
		CurrentValue json.RawMessage `json:"currentValue"`
		CashPnl      json.RawMessage `json:"cashPnl"`
		PercentPnl   json.RawMessage `json:"percentPnl"`
		RealizedPnl  json.RawMessage `json:"realizedPnl"`
		Title        string          `json:"title"`
		Outcome      string          `json:"outcome"`
		EndDate      string          `json:"endDate"`
		Redeemable   bool            `json:"redeemable"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	size, _ := parseFlexibleFloat(raw.Size)
	avgPrice, _ := parseFlexibleFloat(raw.AvgPrice)
	initialValue, _ := parseFlexibleFloat(raw.InitialValue)
	currentValue, _ := parseFlexibleFloat(raw.CurrentValue)
	cashPnL, _ := parseFlexibleFloat(raw.CashPnl)
	percentPnL, _ := parseFlexibleFloat(raw.PercentPnl)
	realizedPnL, _ := parseFlexibleFloat(raw.RealizedPnl)
	lastUpdated := time.Time{}
	if parsed, err := time.Parse(time.RFC3339, raw.EndDate); err == nil {
		lastUpdated = parsed
	}

	*p = UserPosition{
		ID:           raw.Asset,
		UserID:       raw.ProxyWallet,
		MarketID:     raw.ConditionID,
		MarketTitle:  raw.Title,
		Outcome:      strings.ToUpper(raw.Outcome),
		Position:     size,
		AveragePrice: avgPrice,
		TotalCost:    initialValue,
		CurrentValue: currentValue,
		PnL:          cashPnL,
		PnLPercent:   percentPnL,
		RealizedPnL:  realizedPnL,
		LastUpdated:  lastUpdated,
		IsClosed:     !raw.Redeemable && size == 0,
	}
	return nil
}

type Trade struct {
	ID          string    `json:"id"`
	MarketID    string    `json:"market_id"`
	MarketTitle string    `json:"market_title"`
	UserID      string    `json:"user_id"`
	Side        string    `json:"side"`
	Outcome     string    `json:"outcome"`
	Count       float64   `json:"count"`
	Price       float64   `json:"price"`
	Fees        float64   `json:"fees"`
	Timestamp   time.Time `json:"timestamp"`
	TxHash      string    `json:"tx_hash"`
}

func (t *Trade) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID              string          `json:"id"`
		ProxyWallet     string          `json:"proxyWallet"`
		ConditionID     string          `json:"conditionId"`
		Title           string          `json:"title"`
		Side            string          `json:"side"`
		Outcome         string          `json:"outcome"`
		Size            json.RawMessage `json:"size"`
		Price           json.RawMessage `json:"price"`
		Fee             json.RawMessage `json:"fee"`
		Timestamp       json.RawMessage `json:"timestamp"`
		TransactionHash string          `json:"transactionHash"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	size, _ := parseFlexibleFloat(raw.Size)
	price, _ := parseFlexibleFloat(raw.Price)
	fee, _ := parseFlexibleFloat(raw.Fee)
	timestamp := time.Time{}
	if epoch, err := parseUnixTime(raw.Timestamp); err == nil && epoch > 0 {
		if epoch > 1_000_000_000_000 {
			timestamp = time.UnixMilli(epoch).UTC()
		} else {
			timestamp = time.Unix(epoch, 0).UTC()
		}
	}

	*t = Trade{
		ID:          raw.ID,
		MarketID:    raw.ConditionID,
		MarketTitle: raw.Title,
		UserID:      raw.ProxyWallet,
		Side:        strings.ToUpper(raw.Side),
		Outcome:     strings.ToUpper(raw.Outcome),
		Count:       size,
		Price:       price,
		Fees:        fee,
		Timestamp:   timestamp,
		TxHash:      raw.TransactionHash,
	}
	return nil
}

type UserActivity struct {
	UserID          string    `json:"user_id"`
	TotalTrades     int       `json:"total_trades"`
	TotalVolume     float64   `json:"total_volume"`
	TotalMarkets    int       `json:"total_markets"`
	ActivePositions int       `json:"active_positions"`
	ClosedPositions int       `json:"closed_positions"`
	TotalPnL        float64   `json:"total_pnl"`
	WinRate         float64   `json:"win_rate"`
	LastActivity    time.Time `json:"last_activity"`
	AccountCreated  time.Time `json:"account_created"`
}

type HolderData struct {
	Address      string  `json:"address"`
	Position     float64 `json:"position"`
	Percentage   float64 `json:"percentage"`
	AveragePrice float64 `json:"average_price"`
	CurrentValue float64 `json:"current_value"`
}

type OpenInterestData struct {
	MarketID     string    `json:"market_id"`
	OpenInterest float64   `json:"open_interest"`
	YesOI        float64   `json:"yes_oi"`
	NoOI         float64   `json:"no_oi"`
	LastUpdated  time.Time `json:"last_updated"`
}

type LeaderboardEntry struct {
	Rank        int     `json:"rank"`
	UserID      string  `json:"user_id"`
	Username    string  `json:"username"`
	TotalPnL    float64 `json:"total_pnl"`
	WinRate     float64 `json:"win_rate"`
	TotalVolume float64 `json:"total_volume"`
	TradeCount  int     `json:"trade_count"`
}

type BuilderAnalytics struct {
	BuilderID     string    `json:"builder_id"`
	BuilderName   string    `json:"builder_name"`
	TotalMarkets  int       `json:"total_markets"`
	TotalVolume   float64   `json:"total_volume"`
	TotalFees     float64   `json:"total_fees"`
	ActiveMarkets int       `json:"active_markets"`
	MarketShare   float64   `json:"market_share"`
	LastUpdated   time.Time `json:"last_updated"`
}

type GetUserPositionsParams struct {
	UserID        string
	Limit         int
	Offset        int
	IsClosed      *bool
	MarketID      string
	SizeThreshold float64
	Redeemable    *bool
}

type GetUserPositionsResponse struct {
	Positions  []UserPosition
	NextCursor string
	HasMore    bool
}

func (r *GetUserPositionsResponse) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var positions []UserPosition
		if err := json.Unmarshal(data, &positions); err != nil {
			return err
		}
		r.Positions = positions
		return nil
	}
	type alias GetUserPositionsResponse
	var wrapped alias
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*r = GetUserPositionsResponse(wrapped)
	return nil
}

func (s *DataService) GetUserPositions(ctx context.Context, params *GetUserPositionsParams) (*GetUserPositionsResponse, error) {
	endpoint := "/positions"
	queryParams := make(map[string]string)

	if params != nil {
		if params.UserID == "" {
			return nil, fmt.Errorf("user address is required")
		}
		queryParams["user"] = params.UserID
		if params.Limit > 0 {
			queryParams["limit"] = strconv.Itoa(params.Limit)
		}
		if params.Offset > 0 {
			queryParams["offset"] = strconv.Itoa(params.Offset)
		}
		if params.MarketID != "" {
			queryParams["market"] = params.MarketID
		}
		if params.SizeThreshold > 0 {
			queryParams["sizeThreshold"] = formatFloat(params.SizeThreshold)
		}
		if params.Redeemable != nil {
			queryParams["redeemable"] = strconv.FormatBool(*params.Redeemable)
		}
		if params.IsClosed != nil && *params.IsClosed {
			endpoint = "/closed-positions"
		}
	}

	var resp GetUserPositionsResponse
	err := s.client.get(ctx, s.client.dataClient, endpoint, queryParams, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

type GetTradesParams struct {
	UserID   string
	MarketID string
	Limit    int
	Offset   int
	Side     string
}

type GetTradesResponse struct {
	Trades     []Trade
	NextCursor string
	HasMore    bool
}

func (r *GetTradesResponse) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var trades []Trade
		if err := json.Unmarshal(data, &trades); err != nil {
			return err
		}
		r.Trades = trades
		return nil
	}
	type alias GetTradesResponse
	var wrapped alias
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*r = GetTradesResponse(wrapped)
	return nil
}

func (s *DataService) GetTrades(ctx context.Context, params *GetTradesParams) (*GetTradesResponse, error) {
	endpoint := "/trades"
	queryParams := make(map[string]string)

	if params != nil {
		if params.UserID != "" {
			queryParams["user"] = params.UserID
		}
		if params.MarketID != "" {
			queryParams["market"] = params.MarketID
		}
		if params.Limit > 0 {
			queryParams["limit"] = strconv.Itoa(params.Limit)
		}
		if params.Offset > 0 {
			queryParams["offset"] = strconv.Itoa(params.Offset)
		}
		if params.Side != "" {
			queryParams["sideType"] = strings.ToUpper(params.Side)
		}
	}

	var resp GetTradesResponse
	err := s.client.get(ctx, s.client.dataClient, endpoint, queryParams, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

func (s *DataService) GetUserActivity(ctx context.Context, userID string) (*UserActivity, error) {
	positions, err := s.GetUserPositions(ctx, &GetUserPositionsParams{UserID: userID, Limit: 500})
	if err != nil {
		return nil, err
	}
	trades, err := s.GetTrades(ctx, &GetTradesParams{UserID: userID, Limit: 500})
	if err != nil {
		return nil, err
	}

	markets := make(map[string]struct{})
	activePositions := 0
	closedPositions := 0
	totalPnL := 0.0
	for _, position := range positions.Positions {
		if position.MarketID != "" {
			markets[position.MarketID] = struct{}{}
		}
		if position.IsClosed {
			closedPositions++
		}
		if position.Position > 0 {
			activePositions++
		}
		totalPnL += position.PnL
	}

	totalVolume := 0.0
	lastActivity := time.Time{}
	for _, trade := range trades.Trades {
		if trade.MarketID != "" {
			markets[trade.MarketID] = struct{}{}
		}
		totalVolume += trade.Count * trade.Price
		if trade.Timestamp.After(lastActivity) {
			lastActivity = trade.Timestamp
		}
	}

	return &UserActivity{
		UserID:          userID,
		TotalTrades:     len(trades.Trades),
		TotalVolume:     totalVolume,
		TotalMarkets:    len(markets),
		ActivePositions: activePositions,
		ClosedPositions: closedPositions,
		TotalPnL:        totalPnL,
		LastActivity:    lastActivity,
	}, nil
}

func (s *DataService) GetTopHolders(ctx context.Context, marketID string) ([]HolderData, error) {
	endpoint := "/holders/" + marketID
	var holders []HolderData
	err := s.client.get(ctx, s.client.dataClient, endpoint, nil, &holders)
	if err != nil {
		return nil, err
	}
	return holders, nil
}

func (s *DataService) GetOpenInterest(ctx context.Context, marketID string) (*OpenInterestData, error) {
	endpoint := "/open-interest/" + marketID
	var oi OpenInterestData
	err := s.client.get(ctx, s.client.dataClient, endpoint, nil, &oi)
	if err != nil {
		return nil, err
	}
	return &oi, nil
}

type GetLeaderboardParams struct {
	Period string `json:"period,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
	SortBy string `json:"sort_by,omitempty"`
}

type GetLeaderboardResponse struct {
	Entries []LeaderboardEntry `json:"entries"`
	Total   int                `json:"total"`
}

func (s *DataService) GetLeaderboard(ctx context.Context, params *GetLeaderboardParams) (*GetLeaderboardResponse, error) {
	endpoint := "/leaderboard"
	queryParams := make(map[string]string)

	if params != nil {
		if params.Period != "" {
			queryParams["period"] = params.Period
		}
		if params.Limit > 0 {
			queryParams["limit"] = strconv.Itoa(params.Limit)
		}
		if params.Offset > 0 {
			queryParams["offset"] = strconv.Itoa(params.Offset)
		}
		if params.SortBy != "" {
			queryParams["sort_by"] = params.SortBy
		}
	}

	var resp GetLeaderboardResponse
	err := s.client.get(ctx, s.client.dataClient, endpoint, queryParams, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

func (s *DataService) GetTotalPositionValue(ctx context.Context, userID string) (float64, error) {
	positions, err := s.GetUserPositions(ctx, &GetUserPositionsParams{UserID: userID, Limit: 500})
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, position := range positions.Positions {
		total += position.CurrentValue
	}
	return total, nil
}

func (s *DataService) GetTotalMarketsTraded(ctx context.Context, userID string) (int, error) {
	activity, err := s.GetUserActivity(ctx, userID)
	if err != nil {
		return 0, err
	}
	return activity.TotalMarkets, nil
}

func (s *DataService) GetPositionsForMarket(ctx context.Context, userID, marketID string) ([]UserPosition, error) {
	resp, err := s.GetUserPositions(ctx, &GetUserPositionsParams{
		UserID:   userID,
		MarketID: marketID,
		Limit:    500,
	})
	if err != nil {
		return nil, err
	}
	return resp.Positions, nil
}

type GetBuilderLeaderboardParams struct {
	Period string `json:"period,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type BuilderLeaderboardEntry struct {
	Rank        int     `json:"rank"`
	BuilderID   string  `json:"builder_id"`
	BuilderName string  `json:"builder_name"`
	TotalVolume float64 `json:"total_volume"`
	TotalFees   float64 `json:"total_fees"`
	MarketCount int     `json:"market_count"`
}

type GetBuilderLeaderboardResponse struct {
	Entries []BuilderLeaderboardEntry `json:"entries"`
}

func (s *DataService) GetBuilderLeaderboard(ctx context.Context, params *GetBuilderLeaderboardParams) (*GetBuilderLeaderboardResponse, error) {
	endpoint := "/builders/leaderboard"
	queryParams := make(map[string]string)

	if params != nil {
		if params.Period != "" {
			queryParams["period"] = params.Period
		}
		if params.Limit > 0 {
			queryParams["limit"] = strconv.Itoa(params.Limit)
		}
	}

	var resp GetBuilderLeaderboardResponse
	err := s.client.get(ctx, s.client.dataClient, endpoint, queryParams, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

type VolumeTimeSeries struct {
	Timestamp time.Time `json:"timestamp"`
	Volume    float64   `json:"volume"`
}

type GetBuilderVolumeParams struct {
	BuilderID string    `json:"builder_id,omitempty"`
	StartTime time.Time `json:"start_time,omitempty"`
	EndTime   time.Time `json:"end_time,omitempty"`
	Interval  string    `json:"interval,omitempty"`
}

type GetBuilderVolumeResponse struct {
	Data []VolumeTimeSeries `json:"data"`
}

func (s *DataService) GetBuilderVolumeTimeSeries(ctx context.Context, params *GetBuilderVolumeParams) (*GetBuilderVolumeResponse, error) {
	endpoint := "/builders/volume"
	queryParams := make(map[string]string)

	if params != nil {
		if params.BuilderID != "" {
			queryParams["builder_id"] = params.BuilderID
		}
		if !params.StartTime.IsZero() {
			queryParams["start_time"] = strconv.FormatInt(params.StartTime.Unix(), 10)
		}
		if !params.EndTime.IsZero() {
			queryParams["end_time"] = strconv.FormatInt(params.EndTime.Unix(), 10)
		}
		if params.Interval != "" {
			queryParams["interval"] = params.Interval
		}
	}

	var resp GetBuilderVolumeResponse
	err := s.client.get(ctx, s.client.dataClient, endpoint, queryParams, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}
