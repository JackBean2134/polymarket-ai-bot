package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type GammaService struct {
	client *Client
}

func NewGammaService(client *Client) *GammaService {
	return &GammaService{client: client}
}

type Event struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle"`
	Category    string    `json:"category"`
	Status      string    `json:"status"`
	StartDate   time.Time `json:"startDate"`
	EndDate     time.Time `json:"endDate"`
	Markets     []Market  `json:"markets"`
	ImageURL    string    `json:"image"`
	Description string    `json:"description"`
	NegRisk     bool      `json:"negRisk"`
}

type Market struct {
	ID                  string    `json:"id"`
	Slug                string    `json:"slug"`
	Title               string    `json:"title"`
	Question            string    `json:"question"`
	Subtitle            string    `json:"subtitle"`
	EventID             string    `json:"event_id"`
	Status              string    `json:"status"`
	OutcomeType         string    `json:"outcome_type"`
	Outcomes            []string  `json:"outcomes"`
	OutcomePrices       []float64 `json:"outcome_prices"`
	YesPrice            float64   `json:"yes_price"`
	NoPrice             float64   `json:"no_price"`
	LastPrice           float64   `json:"last_price"`
	Volume              float64   `json:"volume"`
	Volume24h           float64   `json:"volume_24h"`
	OpenInterest        float64   `json:"open_interest"`
	Liquidity           float64   `json:"liquidity"`
	ExpirationTime      time.Time `json:"expiration_time"`
	ExpirationValue     string    `json:"expiration_value"`
	Result              string    `json:"result"`
	CanClose            bool      `json:"can_close"`
	ImageURL            string    `json:"image_url"`
	Description         string    `json:"description"`
	ResolutionSource    string    `json:"resolution_source"`
	UMAResolutionStatus string    `json:"uma_resolution_status"`
	GroupItemTitle      string    `json:"group_item_title"`
	ClosedTime          time.Time `json:"closed_time"`
	ClobTokenIDs        []string  `json:"clob_token_ids"`
	Active              bool      `json:"active"`
	Closed              bool      `json:"closed"`
	AcceptingOrders     bool      `json:"accepting_orders"`
	NegRisk             bool      `json:"neg_risk"`
	TakerBaseFee        int       `json:"taker_base_fee"`
	MakerBaseFee        int       `json:"maker_base_fee"`
	TickSize            float64   `json:"tick_size"`
	MinOrderSize        float64   `json:"min_order_size"`
}

// gammaMarketRaw mirrors the real Gamma payload. Numeric and array fields are
// often serialized as strings, so they are decoded through the flexible
// helpers in decimal.go.
type gammaMarketRaw struct {
	ID                  string          `json:"id"`
	Slug                string          `json:"slug"`
	Title               string          `json:"title"`
	Question            string          `json:"question"`
	Subtitle            string          `json:"subtitle"`
	EventID             string          `json:"event_id"`
	Status              string          `json:"status"`
	OutcomeType         string          `json:"outcomeType"`
	Outcomes            json.RawMessage `json:"outcomes"`
	OutcomePrices       json.RawMessage `json:"outcomePrices"`
	Volume              json.RawMessage `json:"volume"`
	Volume24hr          json.RawMessage `json:"volume24hr"`
	OpenInterest        json.RawMessage `json:"openInterest"`
	Liquidity           json.RawMessage `json:"liquidity"`
	EndDate             string          `json:"endDate"`
	EndDateIso          string          `json:"endDateIso"`
	ClosedTime          string          `json:"closedTime"`
	Result              string          `json:"result"`
	Image               string          `json:"image"`
	Icon                string          `json:"icon"`
	Description         string          `json:"description"`
	ResolutionSource    string          `json:"resolutionSource"`
	UMAResolutionStatus string          `json:"umaResolutionStatus"`
	GroupItemTitle      string          `json:"groupItemTitle"`
	ClobTokenIds        json.RawMessage `json:"clobTokenIds"`
	Active              bool            `json:"active"`
	Closed              bool            `json:"closed"`
	AcceptingOrders     bool            `json:"acceptingOrders"`
	NegRisk             bool            `json:"negRisk"`
	TakerBaseFee        json.RawMessage `json:"takerBaseFee"`
	MakerBaseFee        json.RawMessage `json:"makerBaseFee"`
	TickSize            json.RawMessage `json:"orderPriceMinTickSize"`
	MinOrderSize        json.RawMessage `json:"orderMinSize"`
	Events              []struct {
		ID string `json:"id"`
	} `json:"events"`
}

func parseGammaTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999-07",
		"2006-01-02 15:04:05-07",
		"2006-01-02 15:04:05.999999-07:00",
		"2006-01-02 15:04:05-07:00",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

// ResolveBinaryOutcome returns YES/NO for a settled binary market. It refuses
// ambiguous or canceled markets instead of guessing. YES means the first
// outcome/first CLOB token, which is usually "Yes" but may be "Over" for a
// totals market; the CSV uses that same first-token convention.
func ResolveBinaryOutcome(market *Market) (string, error) {
	if market == nil {
		return "", fmt.Errorf("market is nil")
	}
	if !market.Closed {
		return "", fmt.Errorf("market is not closed")
	}
	if len(market.Outcomes) != 2 || len(market.OutcomePrices) != 2 {
		return "", fmt.Errorf("market is not binary")
	}
	yes, no := market.OutcomePrices[0], market.OutcomePrices[1]
	if yes >= 0.99 && no <= 0.01 {
		return "YES", nil
	}
	if no >= 0.99 && yes <= 0.01 {
		return "NO", nil
	}
	return "", fmt.Errorf("outcome prices are not final: %v/%v", yes, no)
}

func (m *Market) UnmarshalJSON(data []byte) error {
	var raw gammaMarketRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	outcomes, err := parseStringArray(raw.Outcomes)
	if err != nil {
		return fmt.Errorf("market %s outcomes: %w", raw.ID, err)
	}
	outcomePrices, err := parseFloatArray(raw.OutcomePrices)
	if err != nil {
		return fmt.Errorf("market %s outcomePrices: %w", raw.ID, err)
	}
	clobTokenIDs, err := parseStringArray(raw.ClobTokenIds)
	if err != nil {
		return fmt.Errorf("market %s clobTokenIds: %w", raw.ID, err)
	}
	volume, err := parseFlexibleFloat(raw.Volume)
	if err != nil {
		return fmt.Errorf("market %s volume: %w", raw.ID, err)
	}
	volume24h, err := parseFlexibleFloat(raw.Volume24hr)
	if err != nil {
		return fmt.Errorf("market %s volume24hr: %w", raw.ID, err)
	}
	openInterest, err := parseFlexibleFloat(raw.OpenInterest)
	if err != nil {
		return fmt.Errorf("market %s openInterest: %w", raw.ID, err)
	}
	liquidity, err := parseFlexibleFloat(raw.Liquidity)
	if err != nil {
		return fmt.Errorf("market %s liquidity: %w", raw.ID, err)
	}
	takerFee, err := parseFlexibleFloat(raw.TakerBaseFee)
	if err != nil {
		return fmt.Errorf("market %s takerBaseFee: %w", raw.ID, err)
	}
	makerFee, err := parseFlexibleFloat(raw.MakerBaseFee)
	if err != nil {
		return fmt.Errorf("market %s makerBaseFee: %w", raw.ID, err)
	}
	tickSize, err := parseFlexibleFloat(raw.TickSize)
	if err != nil {
		return fmt.Errorf("market %s tickSize: %w", raw.ID, err)
	}
	minOrderSize, err := parseFlexibleFloat(raw.MinOrderSize)
	if err != nil {
		return fmt.Errorf("market %s minOrderSize: %w", raw.ID, err)
	}

	title := raw.Question
	if title == "" {
		title = raw.Title
	}
	imageURL := raw.Image
	if imageURL == "" {
		imageURL = raw.Icon
	}
	eventID := raw.EventID
	if eventID == "" && len(raw.Events) > 0 {
		eventID = raw.Events[0].ID
	}

	expiration := time.Time{}
	if raw.EndDate != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, raw.EndDate); parseErr == nil {
			expiration = parsed
		}
	}
	if expiration.IsZero() && raw.EndDateIso != "" {
		if parsed, parseErr := time.Parse("2006-01-02", raw.EndDateIso); parseErr == nil {
			expiration = parsed
		}
	}
	closedTime := parseGammaTime(raw.ClosedTime)

	status := raw.Status
	if status == "" {
		switch {
		case raw.Closed:
			status = "closed"
		case raw.Active:
			status = "active"
		default:
			status = "inactive"
		}
	}

	outcomeType := raw.OutcomeType
	if outcomeType == "" && len(outcomes) == 2 {
		outcomeType = "binary"
	}

	*m = Market{
		ID:                  raw.ID,
		Slug:                raw.Slug,
		Title:               title,
		Question:            raw.Question,
		Subtitle:            raw.Subtitle,
		EventID:             eventID,
		Status:              status,
		OutcomeType:         outcomeType,
		Outcomes:            outcomes,
		OutcomePrices:       outcomePrices,
		Volume:              volume,
		Volume24h:           volume24h,
		OpenInterest:        openInterest,
		Liquidity:           liquidity,
		ExpirationTime:      expiration,
		Result:              raw.Result,
		ExpirationValue:     raw.Result,
		CanClose:            raw.AcceptingOrders,
		ImageURL:            imageURL,
		Description:         raw.Description,
		ResolutionSource:    raw.ResolutionSource,
		UMAResolutionStatus: raw.UMAResolutionStatus,
		GroupItemTitle:      raw.GroupItemTitle,
		ClosedTime:          closedTime,
		ClobTokenIDs:        clobTokenIDs,
		Active:              raw.Active,
		Closed:              raw.Closed,
		AcceptingOrders:     raw.AcceptingOrders,
		NegRisk:             raw.NegRisk,
		TakerBaseFee:        int(takerFee),
		MakerBaseFee:        int(makerFee),
		TickSize:            tickSize,
		MinOrderSize:        minOrderSize,
	}
	if len(outcomePrices) > 0 {
		m.YesPrice = clampProbability(outcomePrices[0])
		m.LastPrice = m.YesPrice
	}
	if len(outcomePrices) > 1 {
		m.NoPrice = clampProbability(outcomePrices[1])
	} else if len(outcomePrices) == 1 {
		m.NoPrice = 1 - m.YesPrice
	}

	return nil
}

// BinaryTokenIDs returns YES and NO CLOB token IDs for a binary market.
func (m *Market) BinaryTokenIDs() (yesTokenID, noTokenID string, err error) {
	if m == nil {
		return "", "", fmt.Errorf("market is nil")
	}
	if len(m.ClobTokenIDs) < 2 {
		return "", "", fmt.Errorf("market %s does not expose two CLOB token IDs", m.ID)
	}
	if len(m.Outcomes) == 2 && !strings.EqualFold(m.Outcomes[0], "yes") {
		return m.ClobTokenIDs[1], m.ClobTokenIDs[0], nil
	}
	return m.ClobTokenIDs[0], m.ClobTokenIDs[1], nil
}

type Tag struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url"`
}

type Series struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url"`
}

type GammaComment struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	Likes     int       `json:"likes"`
	MarketID  string    `json:"market_id"`
}

type Sport struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Slug       string   `json:"slug"`
	Categories []string `json:"categories"`
	ImageURL   string   `json:"image_url"`
}

type ListEventsParams struct {
	Slug         string
	TagSlug      string
	Search       string
	Category     string
	Status       string
	Active       *bool
	Closed       *bool
	Limit        int
	Offset       int
	Order        string
	Ascending    *bool
	StartDateMin string
	EndDateMin   string
}

type ListEventsResponse struct {
	Events     []Event
	NextCursor string
	HasMore    bool
}

func (r *ListEventsResponse) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var events []Event
		if err := json.Unmarshal(data, &events); err != nil {
			return err
		}
		r.Events = events
		return nil
	}

	type alias ListEventsResponse
	var wrapped alias
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*r = ListEventsResponse(wrapped)
	return nil
}

func (s *GammaService) ListEvents(ctx context.Context, params *ListEventsParams) (*ListEventsResponse, error) {
	endpoint := "/events"
	queryParams := make(map[string]string)

	if params != nil {
		if params.Slug != "" {
			queryParams["slug"] = params.Slug
		}
		if params.TagSlug != "" {
			queryParams["tag_slug"] = params.TagSlug
		}
		if params.Search != "" {
			queryParams["search"] = params.Search
		}
		if params.Category != "" {
			queryParams["category"] = params.Category
		}
		if params.Status != "" {
			queryParams["status"] = params.Status
		}
		if params.Active != nil {
			queryParams["active"] = strconv.FormatBool(*params.Active)
		}
		if params.Closed != nil {
			queryParams["closed"] = strconv.FormatBool(*params.Closed)
		}
		if params.Limit > 0 {
			queryParams["limit"] = strconv.Itoa(params.Limit)
		}
		if params.Offset > 0 {
			queryParams["offset"] = strconv.Itoa(params.Offset)
		}
		if params.Order != "" {
			queryParams["order"] = params.Order
		}
		if params.Ascending != nil {
			queryParams["ascending"] = strconv.FormatBool(*params.Ascending)
		}
		if params.StartDateMin != "" {
			queryParams["start_date_min"] = params.StartDateMin
		}
		if params.EndDateMin != "" {
			queryParams["end_date_min"] = params.EndDateMin
		}
	}

	var resp ListEventsResponse
	err := s.client.get(ctx, s.client.gammaClient, endpoint, queryParams, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

func (s *GammaService) GetEventByID(ctx context.Context, eventID string) (*Event, error) {
	endpoint := "/events/" + eventID
	var event Event
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &event)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func (s *GammaService) GetEventBySlug(ctx context.Context, slug string) (*Event, error) {
	resp, err := s.ListEvents(ctx, &ListEventsParams{Slug: slug, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(resp.Events) == 0 {
		return nil, fmt.Errorf("event slug %q not found", slug)
	}
	return &resp.Events[0], nil
}

func (s *GammaService) GetEventTags(ctx context.Context, eventID string) ([]Tag, error) {
	endpoint := "/events/" + eventID + "/tags"
	var tags []Tag
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &tags)
	if err != nil {
		return nil, err
	}
	return tags, nil
}

type ListMarketsParams struct {
	EventID      string
	Slug         string
	Search       string
	Category     string
	Status       string
	Active       *bool
	Closed       *bool
	Limit        int
	Offset       int
	Order        string
	Ascending    *bool
	LiquidityMin float64
	VolumeMin    float64
	StartDateMin string
	StartDateMax string
	EndDateMin   string
	EndDateMax   string
}

type ListMarketsResponse struct {
	Markets    []Market
	NextCursor string
	HasMore    bool
}

func (r *ListMarketsResponse) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var markets []Market
		if err := json.Unmarshal(data, &markets); err != nil {
			return err
		}
		r.Markets = markets
		return nil
	}

	type alias ListMarketsResponse
	var wrapped alias
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*r = ListMarketsResponse(wrapped)
	return nil
}

func (s *GammaService) ListMarkets(ctx context.Context, params *ListMarketsParams) (*ListMarketsResponse, error) {
	endpoint := "/markets"
	queryParams := make(map[string]string)

	if params != nil {
		if params.EventID != "" {
			queryParams["event_id"] = params.EventID
		}
		if params.Slug != "" {
			queryParams["slug"] = params.Slug
		}
		if params.Search != "" {
			queryParams["search"] = params.Search
		}
		if params.Category != "" {
			queryParams["category"] = params.Category
		}
		if params.Status != "" {
			queryParams["status"] = params.Status
		}
		if params.Active != nil {
			queryParams["active"] = strconv.FormatBool(*params.Active)
		}
		if params.Closed != nil {
			queryParams["closed"] = strconv.FormatBool(*params.Closed)
		}
		if params.Limit > 0 {
			queryParams["limit"] = strconv.Itoa(params.Limit)
		}
		if params.Offset > 0 {
			queryParams["offset"] = strconv.Itoa(params.Offset)
		}
		if params.Order != "" {
			queryParams["order"] = params.Order
		}
		if params.Ascending != nil {
			queryParams["ascending"] = strconv.FormatBool(*params.Ascending)
		}
		if params.LiquidityMin > 0 {
			queryParams["liquidity_num_min"] = formatFloat(params.LiquidityMin)
		}
		if params.VolumeMin > 0 {
			queryParams["volume_num_min"] = formatFloat(params.VolumeMin)
		}
		if params.StartDateMin != "" {
			queryParams["start_date_min"] = params.StartDateMin
		}
		if params.StartDateMax != "" {
			queryParams["start_date_max"] = params.StartDateMax
		}
		if params.EndDateMin != "" {
			queryParams["end_date_min"] = params.EndDateMin
		}
		if params.EndDateMax != "" {
			queryParams["end_date_max"] = params.EndDateMax
		}
	}

	var resp ListMarketsResponse
	err := s.client.get(ctx, s.client.gammaClient, endpoint, queryParams, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

func (s *GammaService) GetMarketByID(ctx context.Context, marketID string) (*Market, error) {
	endpoint := "/markets/" + marketID
	var market Market
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &market)
	if err != nil {
		return nil, err
	}
	return &market, nil
}

func (s *GammaService) GetMarketBySlug(ctx context.Context, slug string) (*Market, error) {
	resp, err := s.ListMarkets(ctx, &ListMarketsParams{Slug: slug, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(resp.Markets) == 0 {
		return nil, fmt.Errorf("market slug %q not found", slug)
	}
	return &resp.Markets[0], nil
}

func (s *GammaService) GetMarketTagsByID(ctx context.Context, marketID string) ([]Tag, error) {
	endpoint := "/markets/" + marketID + "/tags"
	var tags []Tag
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &tags)
	if err != nil {
		return nil, err
	}
	return tags, nil
}

func (s *GammaService) GetTopHolders(ctx context.Context, marketIDs []string) (map[string][]string, error) {
	endpoint := "/markets/top-holders"
	params := map[string]interface{}{
		"market_ids": marketIDs,
	}
	var result map[string][]string
	err := s.client.post(ctx, s.client.gammaClient, endpoint, params, &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *GammaService) GetOpenInterest(ctx context.Context, marketID string) (float64, error) {
	market, err := s.GetMarketByID(ctx, marketID)
	if err != nil {
		return 0, err
	}
	return market.OpenInterest, nil
}

func (s *GammaService) GetLiveVolume(ctx context.Context, eventID string) (float64, error) {
	endpoint := "/events/" + eventID + "/volume"
	var result struct {
		Volume float64 `json:"volume"`
	}
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &result)
	if err != nil {
		return 0, err
	}
	return result.Volume, nil
}

func (s *GammaService) ListTags(ctx context.Context) ([]Tag, error) {
	endpoint := "/tags"
	var tags []Tag
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &tags)
	if err != nil {
		return nil, err
	}
	return tags, nil
}

func (s *GammaService) GetTagByID(ctx context.Context, tagID string) (*Tag, error) {
	endpoint := "/tags/" + tagID
	var tag Tag
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &tag)
	if err != nil {
		return nil, err
	}
	return &tag, nil
}

func (s *GammaService) GetTagBySlug(ctx context.Context, slug string) (*Tag, error) {
	endpoint := "/tags/slug/" + slug
	var tag Tag
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &tag)
	if err != nil {
		return nil, err
	}
	return &tag, nil
}

func (s *GammaService) ListComments(ctx context.Context, marketID string, limit int) ([]GammaComment, error) {
	endpoint := "/comments"
	params := map[string]string{
		"market_id": marketID,
	}
	if limit > 0 {
		params["limit"] = strconv.Itoa(limit)
	}
	var comments []GammaComment
	err := s.client.get(ctx, s.client.gammaClient, endpoint, params, &comments)
	if err != nil {
		return nil, err
	}
	return comments, nil
}

func (s *GammaService) GetSports(ctx context.Context) ([]Sport, error) {
	endpoint := "/sports"
	var sports []Sport
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &sports)
	if err != nil {
		return nil, err
	}
	return sports, nil
}

func (s *GammaService) GetProfile(ctx context.Context, walletAddress string) (interface{}, error) {
	endpoint := "/profile/" + walletAddress
	var profile interface{}
	err := s.client.get(ctx, s.client.gammaClient, endpoint, nil, &profile)
	if err != nil {
		return nil, err
	}
	return profile, nil
}
