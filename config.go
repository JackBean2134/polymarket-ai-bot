package polymarket

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Polymarket PolymarketConfig `json:"polymarket"`
	Risk       RiskConfig       `json:"risk"`
	AI         AIConfig         `json:"ai"`
	Trading    TradingConfig    `json:"trading"`
	Logging    LoggingConfig    `json:"logging"`
}

type PolymarketConfig struct {
	APIKey        string `json:"api_key"`
	APISecret     string `json:"api_secret"`
	Passphrase    string `json:"passphrase"`
	WalletAddress string `json:"wallet_address"`
	PrivateKey    string `json:"-"`
	FunderAddress string `json:"funder_address"`
	SignatureType int    `json:"signature_type"`
	ChainID       int64  `json:"chain_id"`
	Timeout       int    `json:"timeout"`
	RetryCount    int    `json:"retry_count"`
	RetryWait     int    `json:"retry_wait"`
	Environment   string `json:"environment"`
}

type AIConfig struct {
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	APIKey       string  `json:"api_key"`
	BaseURL      string  `json:"base_url"`
	Temperature  float64 `json:"temperature"`
	MaxTokens    int     `json:"max_tokens"`
	Timeout      int     `json:"timeout"`
	CacheEnabled bool    `json:"cache_enabled"`
	CacheTTL     int     `json:"cache_ttl"`
}

type TradingConfig struct {
	Enabled            bool     `json:"enabled"`
	Mode               string   `json:"mode"`
	AllowLive          bool     `json:"allow_live"`
	AllowedStrategies  []string `json:"allowed_strategies"`
	AllowedMarkets     []string `json:"allowed_markets"`
	AllowedTokens      []string `json:"allowed_tokens"`
	BlockedMarkets     []string `json:"blocked_markets"`
	StopFile           string   `json:"stop_file"`
	HeartbeatFile      string   `json:"heartbeat_file"`
	MinLiquidity       float64  `json:"min_liquidity"`
	MinVolume24h       float64  `json:"min_volume_24h"`
	MaxSlippage        float64  `json:"max_slippage"`
	AutoCompound       bool     `json:"auto_compound"`
	RebalanceThreshold float64  `json:"rebalance_threshold"`
	ScanInterval       int      `json:"scan_interval"`
	MaxOpenPositions   int      `json:"max_open_positions"`
}

type LoggingConfig struct {
	Level      string `json:"level"`
	Format     string `json:"format"`
	Output     string `json:"output"`
	File       string `json:"file"`
	MaxSize    int    `json:"max_size"`
	MaxBackups int    `json:"max_backups"`
	MaxAge     int    `json:"max_age"`
}

func LoadConfig() (*Config, error) {
	configPath := os.Getenv("POLYMARKET_CONFIG")
	if configPath == "" {
		configPath = "polymarket_config.json"
	}

	return LoadConfigFrom(configPath)
}

func LoadConfigFrom(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", configPath, err)
	}

	config := DefaultConfig()
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", configPath, err)
	}

	config.loadFromEnv()

	if err := config.validate(); err != nil {
		return nil, err
	}

	return config, nil
}

func (c *Config) loadFromEnv() {
	if apiKey := os.Getenv("POLYMARKET_API_KEY"); apiKey != "" {
		c.Polymarket.APIKey = apiKey
	}
	if apiSecret := os.Getenv("POLYMARKET_API_SECRET"); apiSecret != "" {
		c.Polymarket.APISecret = apiSecret
	}
	if passphrase := os.Getenv("POLYMARKET_PASSPHRASE"); passphrase != "" {
		c.Polymarket.Passphrase = passphrase
	}
	if walletAddress := os.Getenv("POLYMARKET_WALLET_ADDRESS"); walletAddress != "" {
		c.Polymarket.WalletAddress = walletAddress
	}
	if privateKey := os.Getenv("POLYMARKET_PRIVATE_KEY"); privateKey != "" {
		c.Polymarket.PrivateKey = privateKey
	}
	if funder := os.Getenv("POLYMARKET_FUNDER_ADDRESS"); funder != "" {
		c.Polymarket.FunderAddress = funder
	}
	if signatureType := os.Getenv("POLYMARKET_SIGNATURE_TYPE"); signatureType != "" {
		if val, err := strconv.Atoi(signatureType); err == nil {
			c.Polymarket.SignatureType = val
		}
	}
	if chainID := os.Getenv("POLYMARKET_CHAIN_ID"); chainID != "" {
		if val, err := strconv.ParseInt(chainID, 10, 64); err == nil {
			c.Polymarket.ChainID = val
		}
	}
	if tokens := os.Getenv("POLYMARKET_ALLOWED_TOKENS"); tokens != "" {
		parts := strings.Split(tokens, ",")
		c.Trading.AllowedTokens = c.Trading.AllowedTokens[:0]
		for _, part := range parts {
			if token := strings.TrimSpace(part); token != "" {
				c.Trading.AllowedTokens = append(c.Trading.AllowedTokens, token)
			}
		}
	}
	if stopFile := os.Getenv("POLYMARKET_STOP_FILE"); stopFile != "" {
		c.Trading.StopFile = stopFile
	}
	if heartbeatFile := os.Getenv("POLYMARKET_HEARTBEAT_FILE"); heartbeatFile != "" {
		c.Trading.HeartbeatFile = heartbeatFile
	}

	if timeout := os.Getenv("POLYMARKET_TIMEOUT"); timeout != "" {
		if val, err := strconv.Atoi(timeout); err == nil {
			c.Polymarket.Timeout = val
		}
	}

	if provider := os.Getenv("AI_PROVIDER"); provider != "" {
		c.AI.Provider = provider
	}
	if model := os.Getenv("AI_MODEL"); model != "" {
		c.AI.Model = model
	}
	if apiKey := os.Getenv("AI_API_KEY"); apiKey != "" {
		c.AI.APIKey = apiKey
	}
	if baseURL := os.Getenv("AI_BASE_URL"); baseURL != "" {
		c.AI.BaseURL = baseURL
	}

	if enabled := os.Getenv("TRADING_ENABLED"); enabled != "" {
		c.Trading.Enabled = enabled == "true" || enabled == "1"
	}
	if mode := os.Getenv("TRADING_MODE"); mode != "" {
		c.Trading.Mode = mode
	}

	if totalCapital := os.Getenv("RISK_TOTAL_CAPITAL"); totalCapital != "" {
		if val, err := strconv.ParseFloat(totalCapital, 64); err == nil {
			c.Risk.TotalCapital = val
		}
	}
	if maxPosition := os.Getenv("RISK_MAX_POSITION"); maxPosition != "" {
		if val, err := strconv.ParseFloat(maxPosition, 64); err == nil {
			c.Risk.MaxPositionSize = val
		}
	}
	if dailyLoss := os.Getenv("RISK_DAILY_LOSS_LIMIT"); dailyLoss != "" {
		if val, err := strconv.ParseFloat(dailyLoss, 64); err == nil {
			c.Risk.DailyLossLimit = val
		}
	}

	if level := os.Getenv("LOG_LEVEL"); level != "" {
		c.Logging.Level = level
	}
	if output := os.Getenv("LOG_OUTPUT"); output != "" {
		c.Logging.Output = output
	}
}

func (c *Config) validate() error {
	if c.Polymarket.Timeout == 0 {
		c.Polymarket.Timeout = 30
	}
	if c.Polymarket.RetryCount == 0 {
		c.Polymarket.RetryCount = 3
	}
	if c.Polymarket.RetryWait == 0 {
		c.Polymarket.RetryWait = 1
	}
	if c.Polymarket.Environment == "" {
		c.Polymarket.Environment = "production"
	}

	if c.AI.Provider == "" {
		c.AI.Provider = "qwen"
	}
	if c.AI.Model == "" {
		c.AI.Model = "qwen-plus"
	}
	if c.AI.Temperature == 0 {
		c.AI.Temperature = 0.3
	}
	if c.AI.MaxTokens == 0 {
		c.AI.MaxTokens = 2000
	}
	if c.AI.Timeout == 0 {
		c.AI.Timeout = 30
	}
	if c.AI.CacheTTL == 0 {
		c.AI.CacheTTL = 1800
	}

	if c.Trading.Mode == "" {
		c.Trading.Mode = "paper"
	}
	if c.Trading.Mode != "paper" && c.Trading.Mode != "live" {
		return fmt.Errorf("invalid trading mode %q: expected paper or live", c.Trading.Mode)
	}
	if len(c.Trading.AllowedStrategies) == 0 {
		c.Trading.AllowedStrategies = []string{"VALUE_BET", "ARBITRAGE", "MOMENTUM"}
	}
	if c.Trading.MinLiquidity == 0 {
		c.Trading.MinLiquidity = 5000
	}
	if c.Trading.MinVolume24h == 0 {
		c.Trading.MinVolume24h = 10000
	}
	if c.Trading.MaxSlippage == 0 {
		c.Trading.MaxSlippage = 0.05
	}
	if c.Trading.ScanInterval == 0 {
		c.Trading.ScanInterval = 60
	}
	if c.Trading.MaxOpenPositions == 0 {
		c.Trading.MaxOpenPositions = 10
	}

	if c.Risk.TotalCapital == 0 {
		c.Risk.TotalCapital = 10000
	}
	if c.Risk.MaxPositionSize == 0 {
		c.Risk.MaxPositionSize = 500
	}
	if c.Risk.MaxPositionPercent == 0 {
		c.Risk.MaxPositionPercent = 0.05
	}
	if c.Risk.DailyLossLimit == 0 {
		c.Risk.DailyLossLimit = 500
	}
	if c.Risk.MaxDrawdown == 0 {
		c.Risk.MaxDrawdown = 0.15
	}
	if c.Risk.KellyMultiplier == 0 {
		c.Risk.KellyMultiplier = 0.25
	}
	if c.Risk.MinConfidence == 0 {
		c.Risk.MinConfidence = 50.0
	}
	if c.Risk.DailyTradeLimit == 0 {
		c.Risk.DailyTradeLimit = 20
	}
	if c.Risk.MaxTradeCount == 0 {
		c.Risk.MaxTradeCount = 100
	}
	if c.Risk.MaxOpenPositions == 0 {
		c.Risk.MaxOpenPositions = 10
	}
	if c.Risk.MaxLiquiditySlippage == 0 {
		c.Risk.MaxLiquiditySlippage = 0.05
	}
	if c.Risk.DiversificationLimit == 0 {
		c.Risk.DiversificationLimit = 0.2
	}
	if c.AI.Temperature < 0 || c.AI.Temperature > 2 {
		return fmt.Errorf("ai.temperature must be in [0, 2]")
	}
	if c.AI.MaxTokens < 0 {
		return fmt.Errorf("ai.max_tokens must not be negative")
	}
	if c.Polymarket.RetryCount < 0 {
		return fmt.Errorf("polymarket.retry_count must not be negative")
	}
	if c.Polymarket.SignatureType < SignatureTypeEOA || c.Polymarket.SignatureType > SignatureTypePolyGnosisSafe {
		return fmt.Errorf("polymarket.signature_type must be 0, 1 or 2")
	}
	if c.Polymarket.ChainID == 0 {
		c.Polymarket.ChainID = ChainPolygon
	}
	if _, ok := contractsByChain[c.Polymarket.ChainID]; !ok {
		return fmt.Errorf("polymarket.chain_id must be 137 or 80002")
	}
	for _, strategy := range c.Trading.AllowedStrategies {
		switch strategy {
		case "VALUE_BET", "ARBITRAGE", "MOMENTUM", "HEDGE":
		default:
			return fmt.Errorf("trading.allowed_strategies contains unknown strategy %q", strategy)
		}
	}
	if c.Risk.TotalCapital <= 0 {
		return fmt.Errorf("risk.total_capital must be positive")
	}
	if c.Risk.MaxPositionSize <= 0 {
		return fmt.Errorf("risk.max_position_size must be positive")
	}
	if c.Risk.MaxPositionPercent <= 0 || c.Risk.MaxPositionPercent > 1 {
		return fmt.Errorf("risk.max_position_percent must be in (0, 1]")
	}
	if c.Risk.MaxDrawdown <= 0 || c.Risk.MaxDrawdown > 1 {
		return fmt.Errorf("risk.max_drawdown must be in (0, 1]")
	}
	if c.Risk.KellyMultiplier < 0 || c.Risk.KellyMultiplier > 1 {
		return fmt.Errorf("risk.kelly_multiplier must be in [0, 1]")
	}
	if c.Trading.ScanInterval <= 0 {
		return fmt.Errorf("trading.scan_interval must be positive")
	}
	if c.Trading.StopFile == "" {
		c.Trading.StopFile = "STOP"
	}
	if c.Trading.HeartbeatFile == "" {
		c.Trading.HeartbeatFile = "heartbeat.txt"
	}

	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.Logging.Output == "" {
		c.Logging.Output = "stdout"
	}

	return nil
}

func (c *Config) SaveToFile(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

func DefaultConfig() *Config {
	return &Config{
		Polymarket: PolymarketConfig{
			Timeout:       30,
			RetryCount:    3,
			RetryWait:     1,
			Environment:   "production",
			SignatureType: SignatureTypeEOA,
			ChainID:       ChainPolygon,
		},
		Risk: RiskConfig{
			TotalCapital:         10000,
			MaxPositionSize:      500,
			MaxPositionPercent:   0.05,
			DailyLossLimit:       500,
			MaxDrawdown:          0.15,
			MaxTradeCount:        100,
			DailyTradeLimit:      20,
			MaxOpenPositions:     10,
			KellyMultiplier:      0.25,
			MinConfidence:        50.0,
			MaxLiquiditySlippage: 0.05,
			DiversificationLimit: 0.2,
		},
		AI: AIConfig{
			Provider:     "qwen",
			Model:        "qwen-plus",
			Temperature:  0.3,
			MaxTokens:    2000,
			Timeout:      30,
			CacheEnabled: true,
			CacheTTL:     1800,
		},
		Trading: TradingConfig{
			Enabled:           false,
			Mode:              "paper",
			AllowLive:         false,
			StopFile:          "STOP",
			HeartbeatFile:     "heartbeat.txt",
			MinLiquidity:      5000,
			MinVolume24h:      10000,
			MaxSlippage:       0.05,
			AutoCompound:      false,
			ScanInterval:      60,
			MaxOpenPositions:  10,
			AllowedStrategies: []string{"VALUE_BET", "ARBITRAGE", "MOMENTUM"},
		},
		Logging: LoggingConfig{
			Level:      "info",
			Format:     "json",
			Output:     "stdout",
			MaxSize:    10,
			MaxBackups: 3,
			MaxAge:     30,
		},
	}
}

// DefaultConfigFromEnv builds the safe defaults and then applies environment
// overrides. It is used by read-only/dry-run commands when no config file
// exists, so credentials can still come from POLYMARKET_* variables.
func DefaultConfigFromEnv() *Config {
	config := DefaultConfig()
	config.loadFromEnv()
	return config
}

func (c *Config) GetTimeout() time.Duration {
	return time.Duration(c.Polymarket.Timeout) * time.Second
}

func (c *Config) GetRetryCount() int {
	return c.Polymarket.RetryCount
}

func (c *Config) GetRetryWait() time.Duration {
	return time.Duration(c.Polymarket.RetryWait) * time.Second
}

func (c *Config) IsPaperTrading() bool {
	return c.Trading.Mode == "paper" || !c.Trading.Enabled
}

// LiveOrderAllowed implements the article's pre-authorized envelope plus the
// triple switch: live mode, config allow_live, and POLYMARKET_ALLOW_LIVE=1.
// A non-empty token whitelist is mandatory, and the STOP file must be absent.
func (c *Config) LiveOrderAllowed(token string) error {
	if c.Trading.Mode != "live" || !c.Trading.Enabled {
		return fmt.Errorf("live order requires trading.mode=live and trading.enabled=true")
	}
	if !c.Trading.AllowLive {
		return fmt.Errorf("live order requires trading.allow_live=true")
	}
	envAllow := strings.ToLower(strings.TrimSpace(os.Getenv("POLYMARKET_ALLOW_LIVE")))
	if envAllow != "1" && envAllow != "true" {
		return fmt.Errorf("live order requires POLYMARKET_ALLOW_LIVE=1")
	}
	if len(c.Trading.AllowedTokens) == 0 {
		return fmt.Errorf("live order requires a non-empty trading.allowed_tokens whitelist")
	}
	if !containsToken(c.Trading.AllowedTokens, token) {
		return fmt.Errorf("token %s is not in trading.allowed_tokens", token)
	}
	return CheckStopFile(c.Trading.StopFile)
}

func (c *Config) IsStrategyAllowed(strategy string) bool {
	for _, allowed := range c.Trading.AllowedStrategies {
		if allowed == strategy {
			return true
		}
	}
	return false
}

func (c *Config) IsMarketAllowed(marketID string) bool {
	for _, blocked := range c.Trading.BlockedMarkets {
		if blocked == marketID {
			return false
		}
	}

	if len(c.Trading.AllowedMarkets) == 0 {
		return true
	}

	for _, allowed := range c.Trading.AllowedMarkets {
		if allowed == marketID {
			return true
		}
	}

	return false
}
