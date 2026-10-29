package polymarket

import (
	"context"
	"math"
	"testing"
)

func TestWalkOrderBookVWAPAndPartialFill(t *testing.T) {
	book := &OrderBook{
		Asks: []Order{
			{Price: 0.50, Size: 100},
			{Price: 0.52, Size: 100},
			{Price: 0.55, Size: 100},
		},
	}

	walk, err := WalkOrderBook(book, "BUY", 250, 0.53, 0)
	if err != nil {
		t.Fatal(err)
	}
	if walk.FilledShares != 200 {
		t.Fatalf("expected 200 shares, got %f", walk.FilledShares)
	}
	if math.Abs(walk.NotionalUSD-102) > 1e-9 {
		t.Fatalf("expected $102 notional, got %f", walk.NotionalUSD)
	}
	if math.Abs(walk.VWAP-0.51) > 1e-9 {
		t.Fatalf("expected 0.51 VWAP, got %f", walk.VWAP)
	}
}

func TestWalkOrderBookRespectsLimitAndFees(t *testing.T) {
	book := &OrderBook{
		Asks: []Order{
			{Price: 0.50, Size: 100},
			{Price: 0.52, Size: 100},
		},
	}
	walk, err := WalkOrderBook(book, "BUY", 200, 0.51, 100)
	if err != nil {
		t.Fatal(err)
	}
	if walk.FilledShares != 100 || walk.NotionalUSD != 50 {
		t.Fatalf("limit price was not respected: %+v", walk)
	}
	if math.Abs(walk.FeeUSD-0.5) > 1e-9 {
		t.Fatalf("expected $0.50 fee, got %f", walk.FeeUSD)
	}
}

func TestWalkOrderBookSellsIntoBids(t *testing.T) {
	book := &OrderBook{
		Bids: []Order{
			{Price: 0.60, Size: 50},
			{Price: 0.58, Size: 100},
		},
	}
	walk, err := WalkOrderBook(book, "SELL", 100, 0.59, 0)
	if err != nil {
		t.Fatal(err)
	}
	if walk.FilledShares != 50 || walk.VWAP != 0.60 {
		t.Fatalf("unexpected sell walk: %+v", walk)
	}
}

func TestPaperExecutorPartialFill(t *testing.T) {
	executor := NewPaperExecutor(staticBookProvider{
		book: &OrderBook{Asks: []Order{{Price: 0.40, Size: 25}}},
	}, 0)

	fill, err := executor.Execute(context.Background(), OrderRequest{
		TokenID:    "token",
		Side:       "BUY",
		Shares:     100,
		LimitPrice: 0.45,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fill.Status != "PAPER_PARTIAL_FILLED" || fill.FilledShares != 25 {
		t.Fatalf("unexpected partial fill: %+v", fill)
	}
	if fill.CashUSD != -10 {
		t.Fatalf("expected -$10 cash, got %f", fill.CashUSD)
	}
}

func TestPaperExecutorUsesScheduleFee(t *testing.T) {
	executor := NewPaperExecutor(staticBookProvider{
		book: &OrderBook{Asks: []Order{{Price: 0.50, Size: 100}}},
	}, 0)

	fill, err := executor.Execute(context.Background(), OrderRequest{
		TokenID:     "token",
		Side:        "BUY",
		Shares:      100,
		LimitPrice:  0.50,
		FeeRate:     0.05,
		FeeExponent: 1,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if math.Abs(fill.NotionalUSD-50) > 1e-9 {
		t.Fatalf("expected $50 notional, got %f", fill.NotionalUSD)
	}
	// 100 × 0.05 × 0.5 × 0.5 = 1.25
	if math.Abs(fill.FeeUSD-1.25) > 1e-9 {
		t.Fatalf("expected the protocol fee $1.25, got %f", fill.FeeUSD)
	}
	if math.Abs(fill.CashUSD-(-51.25)) > 1e-9 {
		t.Fatalf("expected -$51.25 cash, got %f", fill.CashUSD)
	}
}
