package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrLiveOrderingDisabled is returned by the legacy CLOBService.CreateOrder
// method. Real orders are built and signed by OrderSigner and submitted by
// LiveExecutor; keep this guard so no caller can accidentally POST an
// unsigned body to /order.
var ErrLiveOrderingDisabled = errors.New("legacy CreateOrder is disabled: use OrderSigner + LiveExecutor for signed orders")

type CLOBService struct {
	client *Client
}

func NewCLOBService(client *Client) *CLOBService {
	return &CLOBService{client: client}
}

type OrderBook struct {
	MarketID    string    `json:"market_id"`
	TokenID     string    `json:"token_id"`
	ConditionID string    `json:"condition_id"`
	Bids        []Order   `json:"bids"`
	Asks        []Order   `json:"asks"`
	Timestamp   time.Time `json:"timestamp"`
	Hash        string    `json:"hash"`
}

type rawOrderBook struct {
	Market    string          `json:"market"`
	AssetID   string          `json:"asset_id"`
	Timestamp json.RawMessage `json:"timestamp"`
	Hash      string          `json:"hash"`
	Bids      []Order         `json:"bids"`
	Asks      []Order         `json:"asks"`
}

func (ob *OrderBook) UnmarshalJSON(data []byte) error {
	var raw rawOrderBook
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	timestamp := time.Time{}
	if value, err := parseUnixTime(raw.Timestamp); err == nil && value > 0 {
		if value > 1_000_000_000_000 {
			timestamp = time.UnixMilli(value).UTC()
		} else {
			timestamp = time.Unix(value, 0).UTC()
		}
	}

	sort.SliceStable(raw.Bids, func(i, j int) bool { return raw.Bids[i].Price > raw.Bids[j].Price })
	sort.SliceStable(raw.Asks, func(i, j int) bool { return raw.Asks[i].Price < raw.Asks[j].Price })

	*ob = OrderBook{
		MarketID:    raw.AssetID,
		TokenID:     raw.AssetID,
		ConditionID: raw.Market,
		Bids:        raw.Bids,
		Asks:        raw.Asks,
		Timestamp:   timestamp,
		Hash:        raw.Hash,
	}
	return nil
}

type Order struct {
	OrderID   string    `json:"order_id,omitempty"`
	Price     float64   `json:"price"`
	Size      float64   `json:"size"`
	Side      string    `json:"side,omitempty"`
	Outcome   string    `json:"outcome,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

type rawOrder struct {
	OrderID string          `json:"order_id"`
	Price   json.RawMessage `json:"price"`
	Size    json.RawMessage `json:"size"`
	Side    string          `json:"side"`
	Outcome string          `json:"outcome"`
}

func (o *Order) UnmarshalJSON(data []byte) error {
	var raw rawOrder
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	price, err := parseFlexibleFloat(raw.Price)
	if err != nil {
		return err
	}
	size, err := parseFlexibleFloat(raw.Size)
	if err != nil {
		return err
	}
	*o = Order{
		OrderID: raw.OrderID,
		Price:   price,
		Size:    size,
		Side:    raw.Side,
		Outcome: raw.Outcome,
	}
	return nil
}

func (o Order) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{
		"order_id": o.OrderID,
		"price":    formatFloat(o.Price),
		"size":     formatFloat(o.Size),
		"side":     o.Side,
		"outcome":  o.Outcome,
	})
}

type MarketPrice struct {
	MarketID  string  `json:"market_id"`
	TokenID   string  `json:"token_id"`
	Side      string  `json:"side"`
	YesPrice  float64 `json:"yes_price"`
	NoPrice   float64 `json:"no_price"`
	LastPrice float64 `json:"last_price"`
	BidPrice  float64 `json:"bid_price"`
	AskPrice  float64 `json:"ask_price"`
	Volume24h float64 `json:"volume_24h"`
	Change24h float64 `json:"change_24h"`
}

func (s *CLOBService) GetOrderBook(ctx context.Context, tokenID string) (*OrderBook, error) {
	if strings.TrimSpace(tokenID) == "" {
		return nil, fmt.Errorf("token ID is required")
	}
	params := map[string]string{"token_id": tokenID}
	var orderBook OrderBook
	if err := s.client.get(ctx, s.client.clobClient, "/book", params, &orderBook); err != nil {
		return nil, err
	}
	return &orderBook, nil
}

func (s *CLOBService) GetTokenPrice(ctx context.Context, tokenID, side string) (float64, error) {
	side = strings.ToUpper(strings.TrimSpace(side))
	if side != "BUY" && side != "SELL" {
		return 0, fmt.Errorf("side must be BUY or SELL")
	}
	params := map[string]string{
		"token_id": tokenID,
		"side":     side,
	}
	var response struct {
		Price Decimal `json:"price"`
	}
	if err := s.client.get(ctx, s.client.clobClient, "/price", params, &response); err != nil {
		return 0, err
	}
	return clampProbability(response.Price.Float64()), nil
}

func (s *CLOBService) GetMidpointPrice(ctx context.Context, tokenID string) (*MarketPrice, error) {
	params := map[string]string{"token_id": tokenID}
	var response struct {
		Mid Decimal `json:"mid"`
	}
	if err := s.client.get(ctx, s.client.clobClient, "/midpoint", params, &response); err != nil {
		return nil, err
	}
	mid := clampProbability(response.Mid.Float64())
	return &MarketPrice{
		MarketID:  tokenID,
		TokenID:   tokenID,
		YesPrice:  mid,
		NoPrice:   clampProbability(1 - mid),
		LastPrice: mid,
	}, nil
}

// GetMarketPrice treats tokenID as the YES token of a binary market and
// returns decimal prices in [0, 1]. It uses the midpoint when available and
// falls back to the best executable ask.
func (s *CLOBService) GetMarketPrice(ctx context.Context, tokenID string) (*MarketPrice, error) {
	if price, err := s.GetMidpointPrice(ctx, tokenID); err == nil && validPrice(price.YesPrice) {
		if book, bookErr := s.GetOrderBook(ctx, tokenID); bookErr == nil {
			if len(book.Bids) > 0 {
				price.BidPrice = book.Bids[0].Price
			}
			if len(book.Asks) > 0 {
				price.AskPrice = book.Asks[0].Price
			}
		}
		return price, nil
	}

	ask, err := s.GetTokenPrice(ctx, tokenID, "BUY")
	if err != nil {
		return nil, err
	}
	return &MarketPrice{
		MarketID:  tokenID,
		TokenID:   tokenID,
		YesPrice:  ask,
		NoPrice:   clampProbability(1 - ask),
		LastPrice: ask,
		AskPrice:  ask,
	}, nil
}

type PriceHistory struct {
	MarketID string              `json:"market_id"`
	Prices   []PriceHistoryPoint `json:"prices"`
	Interval string              `json:"interval"`
}

type PriceHistoryPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Price     float64   `json:"price"`
	Volume    float64   `json:"volume"`
}

type GetPriceHistoryParams struct {
	MarketID  string
	Interval  string
	Fidelity  int
	StartTime int64
	EndTime   int64
	Limit     int
}

func (s *CLOBService) GetPriceHistory(ctx context.Context, params *GetPriceHistoryParams) (*PriceHistory, error) {
	if params == nil || params.MarketID == "" {
		return nil, fmt.Errorf("market/token ID is required")
	}

	queryParams := map[string]string{"market": params.MarketID}
	if params.Interval != "" {
		queryParams["interval"] = params.Interval
	}
	if params.Fidelity > 0 {
		queryParams["fidelity"] = strconv.Itoa(params.Fidelity)
	}
	if params.StartTime > 0 {
		queryParams["startTs"] = strconv.FormatInt(params.StartTime, 10)
	}
	if params.EndTime > 0 {
		queryParams["endTs"] = strconv.FormatInt(params.EndTime, 10)
	}

	var response struct {
		History []struct {
			T int64   `json:"t"`
			P Decimal `json:"p"`
		} `json:"history"`
	}
	if err := s.client.get(ctx, s.client.clobClient, "/prices-history", queryParams, &response); err != nil {
		return nil, err
	}

	history := &PriceHistory{
		MarketID: params.MarketID,
		Interval: params.Interval,
		Prices:   make([]PriceHistoryPoint, 0, len(response.History)),
	}
	for _, point := range response.History {
		history.Prices = append(history.Prices, PriceHistoryPoint{
			Timestamp: time.Unix(point.T, 0).UTC(),
			Price:     clampProbability(point.P.Float64()),
		})
	}
	if params.Limit > 0 && len(history.Prices) > params.Limit {
		history.Prices = history.Prices[len(history.Prices)-params.Limit:]
	}
	return history, nil
}

type TickSize struct {
	TokenID  string  `json:"token_id"`
	TickSize float64 `json:"tick_size"`
}

func (s *CLOBService) GetTickSize(ctx context.Context, tokenID string) (*TickSize, error) {
	params := map[string]string{"token_id": tokenID}
	var response struct {
		MinimumTickSize Decimal `json:"minimum_tick_size"`
	}
	if err := s.client.get(ctx, s.client.clobClient, "/tick-size", params, &response); err != nil {
		return nil, err
	}
	return &TickSize{TokenID: tokenID, TickSize: response.MinimumTickSize.Float64()}, nil
}

func (s *CLOBService) GetNegRisk(ctx context.Context, tokenID string) (bool, error) {
	params := map[string]string{"token_id": tokenID}
	var response struct {
		NegRisk bool `json:"neg_risk"`
	}
	if err := s.client.get(ctx, s.client.clobClient, "/neg-risk", params, &response); err != nil {
		return false, err
	}
	return response.NegRisk, nil
}

func (s *CLOBService) GetFeeRateBps(ctx context.Context, tokenID string) (int, error) {
	params := map[string]string{"token_id": tokenID}
	var response struct {
		BaseFee Decimal `json:"base_fee"`
	}
	if err := s.client.get(ctx, s.client.clobClient, "/fee-rate", params, &response); err != nil {
		return 0, err
	}
	fee := int(response.BaseFee.Float64())
	if fee < 0 {
		return 0, fmt.Errorf("negative fee rate %d", fee)
	}
	return fee, nil
}

type ServerTime struct {
	ISO   string `json:"iso"`
	Epoch int64  `json:"epoch"`
}

func (st *ServerTime) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		type alias ServerTime
		var wrapped alias
		if err := json.Unmarshal(data, &wrapped); err != nil {
			return err
		}
		*st = ServerTime(wrapped)
		return nil
	}
	epoch, err := parseUnixTime(data)
	if err != nil {
		return err
	}
	st.Epoch = epoch
	st.ISO = time.Unix(epoch, 0).UTC().Format(time.RFC3339)
	return nil
}

func (s *CLOBService) GetServerTime(ctx context.Context) (*ServerTime, error) {
	var serverTime ServerTime
	if err := s.client.get(ctx, s.client.clobClient, "/time", nil, &serverTime); err != nil {
		return nil, err
	}
	return &serverTime, nil
}

type CreateOrder struct {
	MarketID   string  `json:"market_id"`
	TokenID    string  `json:"token_id,omitempty"`
	Side       string  `json:"side"`
	Outcome    string  `json:"outcome"`
	Count      float64 `json:"count"`
	Price      float64 `json:"price"`
	OrderType  string  `json:"order_type"`
	Expiration int64   `json:"expiration,omitempty"`
}

type OrderResponse struct {
	OrderID   string    `json:"order_id"`
	Status    string    `json:"status"`
	MarketID  string    `json:"market_id"`
	Side      string    `json:"side"`
	Outcome   string    `json:"outcome"`
	Count     float64   `json:"count"`
	Price     float64   `json:"price"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateOrder fails closed. A real CLOB order is an EIP-712 signature over
// tokenID, price, size, side, salt and expiration, produced by the owner key.
// Sending an unsigned JSON body to /order cannot work and would only create a
// false sense of security.
func (s *CLOBService) CreateOrder(ctx context.Context, order *CreateOrder) (*OrderResponse, error) {
	return nil, ErrLiveOrderingDisabled
}

type UserOrdersParams struct {
	MarketID   string
	ActiveOnly bool
	Limit      int
	Cursor     string
}

type UserOrder struct {
	OrderID   string    `json:"id"`
	MarketID  string    `json:"market"`
	TokenID   string    `json:"asset_id"`
	Side      string    `json:"side"`
	Outcome   string    `json:"outcome"`
	Price     float64   `json:"price"`
	Size      float64   `json:"original_size"`
	Matched   float64   `json:"size_matched"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type UserOrdersResponse struct {
	Orders []UserOrder
}

func (r *UserOrdersResponse) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var orders []UserOrder
		if err := json.Unmarshal(data, &orders); err != nil {
			return err
		}
		r.Orders = orders
		return nil
	}
	type alias UserOrdersResponse
	var wrapped alias
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*r = UserOrdersResponse(wrapped)
	return nil
}

func (s *CLOBService) GetUserOrders(ctx context.Context, params *UserOrdersParams) (*UserOrdersResponse, error) {
	queryParams := make(map[string]string)
	if params != nil {
		if params.MarketID != "" {
			queryParams["market"] = params.MarketID
		}
		if params.Limit > 0 {
			queryParams["limit"] = strconv.Itoa(params.Limit)
		}
		if params.Cursor != "" {
			queryParams["next_cursor"] = params.Cursor
		}
	}
	var response UserOrdersResponse
	if err := s.client.get(ctx, s.client.clobClient, "/data/orders", queryParams, &response); err != nil {
		return nil, err
	}
	if params != nil && params.ActiveOnly {
		filtered := response.Orders[:0]
		for _, order := range response.Orders {
			if strings.EqualFold(order.Status, "LIVE") || strings.EqualFold(order.Status, "OPEN") {
				filtered = append(filtered, order)
			}
		}
		response.Orders = filtered
	}
	return &response, nil
}

func (s *CLOBService) CancelOrder(ctx context.Context, orderID string) error {
	if strings.TrimSpace(orderID) == "" {
		return fmt.Errorf("order ID is required")
	}
	body := map[string]string{"orderID": orderID}
	if err := s.client.delete(ctx, s.client.clobClient, "/order", body, nil); err != nil {
		return err
	}
	return nil
}

type PostOrderPayload struct {
	Order     *SignedOrder `json:"order"`
	Owner     string       `json:"owner"`
	OrderType string       `json:"orderType"`
	DeferExec bool         `json:"deferExec"`
	PostOnly  bool         `json:"postOnly,omitempty"`
}

type PostOrderResponse struct {
	Success      bool     `json:"success"`
	ErrorMsg     string   `json:"errorMsg"`
	OrderID      string   `json:"orderID"`
	OrderHashes  []string `json:"orderHashes"`
	Status       string   `json:"status"`
	TakingAmount string   `json:"takingAmount"`
	MakingAmount string   `json:"makingAmount"`
}

func (s *CLOBService) PostOrder(ctx context.Context, owner string, orderType string, order *SignedOrder) (*PostOrderResponse, error) {
	if order == nil {
		return nil, fmt.Errorf("order is nil")
	}
	if strings.TrimSpace(owner) == "" {
		return nil, fmt.Errorf("API key/owner is required")
	}
	if orderType == "" {
		orderType = "GTC"
	}
	payload := &PostOrderPayload{
		Order:     order,
		Owner:     owner,
		OrderType: strings.ToUpper(orderType),
		DeferExec: false,
	}
	var response PostOrderResponse
	if err := s.client.post(ctx, s.client.clobClient, "/order", payload, &response); err != nil {
		return nil, err
	}
	if strings.TrimSpace(response.ErrorMsg) != "" {
		return nil, fmt.Errorf("order rejected: %s", response.ErrorMsg)
	}
	if strings.TrimSpace(response.OrderID) == "" {
		return nil, fmt.Errorf("order response did not include an order ID")
	}
	return &response, nil
}

type LiveOrder struct {
	ID              string          `json:"id"`
	Status          string          `json:"status"`
	Market          string          `json:"market"`
	AssetID         string          `json:"asset_id"`
	Side            string          `json:"side"`
	Outcome         string          `json:"outcome"`
	Price           Decimal         `json:"price"`
	OriginalSize    Decimal         `json:"original_size"`
	SizeMatched     Decimal         `json:"size_matched"`
	Expiration      string          `json:"expiration"`
	CreatedAt       json.RawMessage `json:"created_at"`
	AssociateTrades []string        `json:"associate_trades"`
}

func (s *CLOBService) GetOrder(ctx context.Context, orderID string) (*LiveOrder, error) {
	if strings.TrimSpace(orderID) == "" {
		return nil, fmt.Errorf("order ID is required")
	}
	var order LiveOrder
	if err := s.client.get(ctx, s.client.clobClient, "/data/order/"+orderID, nil, &order); err != nil {
		return nil, err
	}
	return &order, nil
}
