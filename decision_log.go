package polymarket

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// DecisionRecord is the audit trail the article insists on: signals, fills,
// skips and state. Skip logs are the most valuable because they record risk
// that was avoided.
type DecisionRecord struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	MarketID  string          `json:"market_id,omitempty"`
	Strategy  string          `json:"strategy,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Signal    *TradingSignal  `json:"signal,omitempty"`
	Execution *TradeExecution `json:"execution,omitempty"`
	State     interface{}     `json:"state,omitempty"`
}

type DecisionLogger struct {
	mu   sync.Mutex
	file *os.File
}

func NewDecisionLogger(path string) (*DecisionLogger, error) {
	if path == "" {
		return nil, fmt.Errorf("decision log path is required")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open decision log: %w", err)
	}
	return &DecisionLogger{file: file}, nil
}

func (l *DecisionLogger) Write(record DecisionRecord) error {
	if l == nil || l.file == nil {
		return nil
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode decision: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write decision: %w", err)
	}
	return nil
}

func (l *DecisionLogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
