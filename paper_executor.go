package polymarket

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// OrderRequest is the common input for paper and live execution.
type OrderRequest struct {
	MarketID   string
	TokenID    string
	Side       string
	Outcome    string
	Shares     float64
	LimitPrice float64
	MaxCashUSD float64
	OrderType  string
	NegRisk    bool
}

// ExecutionFill is the result of an order. CashUSD is negative when cash
// leaves the account (BUY) and positive when cash is received (SELL).
type ExecutionFill struct {
	OrderID      string
	Status       string
	FilledShares float64
	AveragePrice float64
	NotionalUSD  float64
	FeeUSD       float64
	CashUSD      float64
	Raw          map[string]interface{}
}

type Executor interface {
	Execute(ctx context.Context, request OrderRequest) (*ExecutionFill, error)
	Cancel(ctx context.Context, orderID string) error
	IsPaper() bool
}

type BookProvider interface {
	GetOrderBook(ctx context.Context, tokenID string) (*OrderBook, error)
}

type bookWalkResult struct {
	FilledShares float64
	NotionalUSD  float64
	FeeUSD       float64
	VWAP         float64
}

// WalkOrderBook fills an order against visible book levels. It does not model
// queue position or latency, but it does model price impact and partial fills.
func WalkOrderBook(book *OrderBook, side string, shares, limitPrice float64, feeBps int) (bookWalkResult, error) {
	if book == nil {
		return bookWalkResult{}, fmt.Errorf("order book is nil")
	}
	if shares <= 0 {
		return bookWalkResult{}, fmt.Errorf("shares must be positive")
	}
	if !validPrice(limitPrice) {
		return bookWalkResult{}, fmt.Errorf("limit price %f is outside (0,1)", limitPrice)
	}
	if feeBps < 0 {
		feeBps = 0
	}

	side = strings.ToUpper(strings.TrimSpace(side))
	var levels []Order
	switch side {
	case "BUY":
		levels = book.Asks
	case "SELL":
		levels = book.Bids
	default:
		return bookWalkResult{}, fmt.Errorf("side must be BUY or SELL")
	}

	remaining := shares
	notional := 0.0
	filled := 0.0
	for _, level := range levels {
		if remaining <= 1e-9 {
			break
		}
		if level.Price <= 0 || level.Price >= 1 || level.Size <= 0 {
			continue
		}
		if side == "BUY" && level.Price > limitPrice+1e-9 {
			continue
		}
		if side == "SELL" && level.Price < limitPrice-1e-9 {
			continue
		}
		take := math.Min(level.Size, remaining)
		filled += take
		notional += take * level.Price
		remaining -= take
	}

	if filled <= 0 {
		return bookWalkResult{}, fmt.Errorf("no executable liquidity at limit %f", limitPrice)
	}
	fee := notional * float64(feeBps) / 10000
	return bookWalkResult{
		FilledShares: filled,
		NotionalUSD:  notional,
		FeeUSD:       fee,
		VWAP:         notional / filled,
	}, nil
}

// PaperExecutor fills orders by walking the live order book. It is still a
// simulation: it assumes you can take the displayed size, without queue or
// latency modelling.
type PaperExecutor struct {
	provider BookProvider
	feeBps   int
}

func NewPaperExecutor(provider BookProvider, feeBps int) *PaperExecutor {
	return &PaperExecutor{provider: provider, feeBps: feeBps}
}

func (p *PaperExecutor) IsPaper() bool {
	return true
}

func (p *PaperExecutor) Execute(ctx context.Context, request OrderRequest) (*ExecutionFill, error) {
	if p == nil || p.provider == nil {
		return nil, fmt.Errorf("paper executor has no book provider")
	}
	if strings.TrimSpace(request.TokenID) == "" {
		return nil, fmt.Errorf("token ID is required")
	}
	side := strings.ToUpper(strings.TrimSpace(request.Side))
	if side != "BUY" && side != "SELL" {
		return nil, fmt.Errorf("side must be BUY or SELL")
	}

	book, err := p.provider.GetOrderBook(ctx, request.TokenID)
	if err != nil {
		return nil, fmt.Errorf("get order book: %w", err)
	}

	shares := request.Shares
	if request.MaxCashUSD > 0 {
		feeMultiplier := 1 + float64(p.feeBps)/10000
		maxShares := request.MaxCashUSD / (request.LimitPrice * feeMultiplier) * 0.999
		if shares > maxShares {
			shares = maxShares
		}
	}
	if shares <= 0 {
		return nil, fmt.Errorf("approved cash limit is below one share")
	}

	walk, err := WalkOrderBook(book, side, shares, request.LimitPrice, p.feeBps)
	if err != nil {
		return nil, err
	}

	status := "PAPER_PARTIAL_FILLED"
	if walk.FilledShares >= shares-1e-9 {
		status = "PAPER_FILLED"
	}

	cash := walk.NotionalUSD + walk.FeeUSD
	if side == "SELL" {
		cash = walk.NotionalUSD - walk.FeeUSD
	} else {
		cash = -(walk.NotionalUSD + walk.FeeUSD)
	}

	return &ExecutionFill{
		OrderID:      fmt.Sprintf("paper-%d", time.Now().UnixNano()),
		Status:       status,
		FilledShares: walk.FilledShares,
		AveragePrice: walk.VWAP,
		NotionalUSD:  walk.NotionalUSD,
		FeeUSD:       walk.FeeUSD,
		CashUSD:      cash,
	}, nil
}

func (p *PaperExecutor) Cancel(ctx context.Context, orderID string) error {
	return nil
}
