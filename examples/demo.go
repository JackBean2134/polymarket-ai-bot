package main

import (
	"fmt"
	"log"

	"github.com/ChainOpera-Network/nofx/polymarket"
)

func main() {
	fmt.Println("=====================================")
	fmt.Println("  Polymarket AI Trading Bot Demo")
	fmt.Println("=====================================")
	fmt.Println()

	// Load configuration
	config, err := polymarket.LoadConfig()
	if err != nil {
		log.Printf("Warning: Could not load config: %v", err)
		config = polymarket.DefaultConfig()
	}

	fmt.Println("✅ Configuration loaded")
	fmt.Printf("   Trading Mode: %s\n", config.Trading.Mode)
	fmt.Printf("   Total Capital: $%.2f\n", config.Risk.TotalCapital)
	fmt.Printf("   Max Position: $%.2f\n", config.Risk.MaxPositionSize)
	fmt.Println()

	riskManager := polymarket.NewRiskManager(&config.Risk)

	fmt.Println("✅ Risk manager initialized (no API calls or trading are performed)")
	fmt.Println()

	// Display risk configuration
	fmt.Println("📊 Risk Configuration:")
	fmt.Printf("   Total Capital: $%.2f\n", config.Risk.TotalCapital)
	fmt.Printf("   Max Position Size: $%.2f\n", config.Risk.MaxPositionSize)
	fmt.Printf("   Max Position Percent: %.1f%%\n", config.Risk.MaxPositionPercent*100)
	fmt.Printf("   Daily Loss Limit: $%.2f\n", config.Risk.DailyLossLimit)
	fmt.Printf("   Max Drawdown: %.1f%%\n", config.Risk.MaxDrawdown*100)
	fmt.Printf("   Kelly Multiplier: %.2f\n", config.Risk.KellyMultiplier)
	fmt.Printf("   Min Confidence: %.1f%%\n", config.Risk.MinConfidence)
	fmt.Println()

	// Display trading configuration
	fmt.Println("⚙️  Trading Configuration:")
	fmt.Printf("   Enabled: %v\n", config.Trading.Enabled)
	fmt.Printf("   Mode: %s\n", config.Trading.Mode)
	fmt.Printf("   Scan Interval: %d seconds\n", config.Trading.ScanInterval)
	fmt.Printf("   Max Open Positions: %d\n", config.Trading.MaxOpenPositions)
	fmt.Printf("   Min Liquidity: $%.2f\n", config.Trading.MinLiquidity)
	fmt.Printf("   Min Volume 24h: $%.2f\n", config.Trading.MinVolume24h)
	fmt.Printf("   Strategies: %v\n", config.Trading.AllowedStrategies)
	fmt.Println()

	// Demo: Show risk metrics
	fmt.Println("📈 Risk Metrics (Initial):")
	metrics := riskManager.GetRiskMetrics()
	fmt.Printf("   Available Capital: $%.2f\n", metrics.AvailableCapital)
	fmt.Printf("   Used Capital: $%.2f\n", metrics.UsedCapital)
	fmt.Printf("   Total PnL: $%.2f\n", metrics.TotalPnL)
	fmt.Printf("   Current Drawdown: %.2f%%\n", metrics.CurrentDrawdown*100)
	fmt.Printf("   Trading Halted: %v\n", metrics.TradingHalted)
	fmt.Println()

	// Demo: Create a sample trading signal
	fmt.Println("🎯 Sample Trading Signal Analysis:")
	fmt.Println()

	sampleSignal := &polymarket.TradingSignal{
		MarketID:      "demo-market-123",
		MarketTitle:   "Will Bitcoin reach $100k in 2026?",
		Strategy:      "VALUE_BET",
		Direction:     "YES",
		Strength:      0.12,
		Confidence:    72.5,
		ExpectedValue: 0.12,
		CurrentPrice:  0.58,
		FairValue:     0.70,
		Assessment: &polymarket.ValueAssessment{
			AIProbability:     0.70,
			MarketImpliedProb: 0.58,
			Edge:              0.12,
			EdgePercent:       20.69,
			Recommendation:    "BUY",
			ConfidenceLevel:   "HIGH",
		},
	}

	fmt.Printf("   Market: %s\n", sampleSignal.MarketTitle)
	fmt.Printf("   Strategy: %s\n", sampleSignal.Strategy)
	fmt.Printf("   Direction: %s\n", sampleSignal.Direction)
	fmt.Printf("   Current Price: %.4f (Implied Prob: %.1f%%)\n",
		sampleSignal.CurrentPrice, sampleSignal.CurrentPrice*100)
	fmt.Printf("   AI Fair Value: %.4f (AI Prob: %.1f%%)\n",
		sampleSignal.FairValue, sampleSignal.Assessment.AIProbability*100)
	fmt.Printf("   Edge: %.2f%%\n", sampleSignal.Assessment.EdgePercent)
	fmt.Printf("   Confidence: %.1f%%\n", sampleSignal.Confidence)
	fmt.Println()

	// Calculate position size
	positionSize := riskManager.CalculatePositionSize(sampleSignal)
	fmt.Println("💰 Position Sizing:")
	fmt.Printf("   Calculated Size: $%.2f\n", positionSize)
	fmt.Printf("   Max Allowed: $%.2f\n", config.Risk.MaxPositionSize)
	fmt.Printf("   Position %% of Capital: %.2f%%\n", (positionSize/config.Risk.TotalCapital)*100)
	fmt.Println()

	// Validate signal
	canTrade := riskManager.CheckTradeLimits(sampleSignal)
	fmt.Println("✅ Risk Checks:")
	fmt.Printf("   Can Trade: %v\n", canTrade)
	fmt.Printf("   Within Position Limits: %v\n", positionSize <= config.Risk.MaxPositionSize)
	fmt.Printf("   Confidence Above Minimum: %v\n", sampleSignal.Confidence >= config.Risk.MinConfidence)
	fmt.Println()

	// Display available strategies
	fmt.Println("🎯 Available Trading Strategies:")
	fmt.Println()

	strategies := []struct {
		name        string
		description string
		condition   string
	}{
		{
			"VALUE_BET",
			"Identifies mispriced markets where AI prediction differs from market odds",
			"Edge > 5%, Confidence > 50%",
		},
		{
			"ARBITRAGE",
			"Exploits order book imbalances and price discrepancies",
			"Expected Value > 3%",
		},
		{
			"MOMENTUM",
			"Follows price trends and volume surges",
			"Trend Strength > 2%, Volume +20%",
		},
		{
			"HEDGE",
			"Automatically hedges profitable positions to lock in gains",
			"Position PnL > 50%",
		},
	}

	for i, s := range strategies {
		fmt.Printf("   %d. %s\n", i+1, s.name)
		fmt.Printf("      Description: %s\n", s.description)
		fmt.Printf("      Entry Condition: %s\n", s.condition)
		fmt.Println()
	}

	fmt.Println("📊 No backtest or live performance history is included with this project.")
	fmt.Println("   The sample signal and position size below are illustrative only.")
	fmt.Println()

	// Next steps
	fmt.Println("🚀 Next Steps:")
	fmt.Println()
	fmt.Println("   1. Keep the default paper mode until you have a calibrated edge")
	fmt.Println("   2. Prepare timestamped predictions, entry prices, and resolved outcomes")
	fmt.Println("   3. Run the CSV backtest and use -arb-scan for read-only opportunity stats")
	fmt.Println("   4. Read LIVE_TRADING.md before using -live-order; it is dry-run by default")
	fmt.Println()

	// Important warnings
	fmt.Println("⚠️  Important Warnings:")
	fmt.Println()
	fmt.Println("   - This is HIGH RISK trading software")
	fmt.Println("   - Only use money you can afford to lose")
	fmt.Println("   - Past performance does not guarantee future results")
	fmt.Println("   - AI predictions are not always accurate")
	fmt.Println("   - Monitor the bot regularly, especially in the beginning")
	fmt.Println("   - Keep your API keys secure and never share them")
	fmt.Println()

	fmt.Println("=====================================")
	fmt.Println("  Demo completed successfully!")
	fmt.Println("=====================================")
	fmt.Println()
	fmt.Println("For detailed documentation, see:")
	fmt.Println("  - README.md - Complete usage guide")
	fmt.Println("  - QUICKSTART.md - Quick start guide")
	fmt.Println("  - ARCHITECTURE.md - System architecture")
	fmt.Println()
	fmt.Println("Ready to start! Run: go run main.go")
	fmt.Println()

}
