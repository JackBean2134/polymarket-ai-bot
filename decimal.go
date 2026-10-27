package polymarket

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Decimal accepts Polymarket's mixed JSON encodings. The public API commonly
// serializes prices, sizes and volumes as strings (for example "0.036")
// while some endpoints return plain numbers. It marshals back as a string,
// which is what the CLOB endpoints accept.
type Decimal float64

func (d *Decimal) UnmarshalJSON(data []byte) error {
	value, err := parseFlexibleFloat(data)
	if err != nil {
		return err
	}
	*d = Decimal(value)
	return nil
}

func (d Decimal) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(strconv.FormatFloat(float64(d), 'f', -1, 64))), nil
}

func (d Decimal) Float64() float64 {
	return float64(d)
}

// parseFlexibleFloat parses a JSON string, number or null into a finite float.
func parseFlexibleFloat(data []byte) (float64, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, nil
	}

	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return 0, fmt.Errorf("decode numeric string: %w", err)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return 0, nil
		}
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, fmt.Errorf("parse numeric string %q: %w", text, err)
		}
		if !isFinite(value) {
			return 0, fmt.Errorf("non-finite numeric value %q", text)
		}
		return value, nil
	}

	value, err := strconv.ParseFloat(string(trimmed), 64)
	if err != nil {
		return 0, fmt.Errorf("parse numeric value %q: %w", string(trimmed), err)
	}
	if !isFinite(value) {
		return 0, fmt.Errorf("non-finite numeric value %q", string(trimmed))
	}
	return value, nil
}

// parseStringArray accepts both a real JSON array and Polymarket's
// double-encoded form: "[\"Yes\", \"No\"]".
func parseStringArray(data []byte) ([]string, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	if trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal(trimmed, &inner); err != nil {
			return nil, fmt.Errorf("decode encoded array: %w", err)
		}
		if strings.TrimSpace(inner) == "" {
			return nil, nil
		}
		return parseStringArray([]byte(inner))
	}

	var values []string
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, fmt.Errorf("decode string array: %w", err)
	}
	return values, nil
}

func parseFloatArray(data []byte) ([]float64, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	if trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal(trimmed, &inner); err != nil {
			return nil, fmt.Errorf("decode encoded price array: %w", err)
		}
		if strings.TrimSpace(inner) == "" {
			return nil, nil
		}
		return parseFloatArray([]byte(inner))
	}

	var rawValues []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawValues); err != nil {
		return nil, fmt.Errorf("decode price array: %w", err)
	}

	values := make([]float64, 0, len(rawValues))
	for _, raw := range rawValues {
		value, err := parseFlexibleFloat(raw)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func parseUnixTime(data []byte) (int64, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, nil
	}

	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return 0, err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return 0, nil
		}
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse unix timestamp %q: %w", text, err)
		}
		return value, nil
	}

	value, err := strconv.ParseInt(string(trimmed), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse unix timestamp %q: %w", string(trimmed), err)
	}
	return value, nil
}

func clampProbability(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
