// Package candles reads Upbit and Bithumb REST candles. One-minute candles
// are the smallest unit both exchanges keep; every amount in this program is
// a sum of them.
package candles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"listing-tracker/internal/market"
	"listing-tracker/internal/restclient"
)

// MaxCount is both exchanges' largest page.
const MaxCount = 200

// KST is fixed so the program does not depend on a time zone database,
// which a Windows host may not have.
var KST = time.FixedZone("KST", 9*60*60)

var baseURLs = map[market.Exchange]string{
	market.Upbit:   "https://api.upbit.com/v1/candles",
	market.Bithumb: "https://api.bithumb.com/v1/candles",
}

// Candle is one official candle. A minute without trades has no candle.
type Candle struct {
	Start       time.Time // UTC start of the candle
	Amount      float64   // candle_acc_trade_price, in the quote currency (KRW)
	Volume      float64   // candle_acc_trade_volume
	Close       float64
	LastTradeAt time.Time
}

type row struct {
	Market   string          `json:"market"`
	UTC      string          `json:"candle_date_time_utc"`
	Close    json.Number     `json:"trade_price"`
	Amount   json.Number     `json:"candle_acc_trade_price"`
	Volume   json.Number     `json:"candle_acc_trade_volume"`
	LastTime json.RawMessage `json:"timestamp"`
}

// Parse reads a candle array and returns it oldest first.
func Parse(payload []byte) ([]Candle, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("candle response is not a JSON array")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var rows []row
	if err := decoder.Decode(&rows); err != nil {
		return nil, err
	}
	result := make([]Candle, 0, len(rows))
	for _, r := range rows {
		start, err := time.Parse("2006-01-02T15:04:05", r.UTC)
		if err != nil {
			return nil, fmt.Errorf("candle time %q: %w", r.UTC, err)
		}
		amount, err := nonnegative(r.Amount)
		if err != nil {
			return nil, fmt.Errorf("candle amount: %w", err)
		}
		volume, err := nonnegative(r.Volume)
		if err != nil {
			return nil, fmt.Errorf("candle volume: %w", err)
		}
		closePrice, _ := nonnegative(r.Close)
		c := Candle{Start: start.UTC(), Amount: amount, Volume: volume, Close: closePrice}
		if ms, err := strconv.ParseInt(string(bytes.Trim(r.LastTime, `"`)), 10, 64); err == nil && ms > 0 {
			c.LastTradeAt = time.UnixMilli(ms).UTC()
		}
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Start.Before(result[j].Start) })
	return result, nil
}

func nonnegative(value json.Number) (float64, error) {
	if value == "" {
		return 0, errors.New("missing value")
	}
	parsed, err := value.Float64()
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid value %q", value)
	}
	return parsed, nil
}

type Fetcher interface {
	Get(context.Context, market.Exchange, restclient.Group, string) ([]byte, error)
}

type Client struct {
	fetcher Fetcher
	now     func() time.Time
}

func NewClient(fetcher Fetcher) *Client { return &Client{fetcher: fetcher, now: time.Now} }

// SetClock replaces the clock, normally with one corrected to the exchanges.
func (c *Client) SetClock(now func() time.Time) { c.now = now }

// formatTo writes the exclusive end time each exchange expects: Upbit takes
// UTC with a zone, Bithumb a KST wall time.
func formatTo(exchange market.Exchange, to time.Time) string {
	if exchange == market.Upbit {
		return to.UTC().Format("2006-01-02T15:04:05Z")
	}
	return to.In(KST).Format("2006-01-02T15:04:05")
}

func (c *Client) get(ctx context.Context, exchange market.Exchange, path, code string, to time.Time, count int) ([]Candle, error) {
	query := url.Values{}
	query.Set("market", code)
	query.Set("count", strconv.Itoa(max(1, min(count, MaxCount))))
	if !to.IsZero() && to.Before(c.now()) {
		query.Set("to", formatTo(exchange, to))
	}
	body, err := c.fetcher.Get(ctx, exchange, restclient.GroupCandle, baseURLs[exchange]+path+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	return Parse(body)
}

// Minutes returns up to count candles of the unit ending before to (zero:
// the latest, including the minute still in progress).
func (c *Client) Minutes(ctx context.Context, exchange market.Exchange, code string, unit int, to time.Time, count int) ([]Candle, error) {
	return c.get(ctx, exchange, "/minutes/"+strconv.Itoa(unit), code, to, count)
}

func (c *Client) Days(ctx context.Context, exchange market.Exchange, code string, count int) ([]Candle, error) {
	return c.get(ctx, exchange, "/days", code, time.Time{}, count)
}

// MinuteRange returns every one-minute candle in [from, to), paging back
// from to. The result says how far back the answer is complete: when the
// exchange returned a short page there is nothing older at all.
func (c *Client) MinuteRange(ctx context.Context, exchange market.Exchange, code string, from, to time.Time) ([]Candle, error) {
	var collected []Candle
	cursor := to
	for page := 0; page < 400; page++ {
		batch, err := c.Minutes(ctx, exchange, code, 1, cursor, MaxCount)
		if err != nil {
			return nil, err
		}
		for _, candle := range batch {
			if !candle.Start.Before(from) && candle.Start.Before(to) {
				collected = append(collected, candle)
			}
		}
		if len(batch) < MaxCount || !batch[0].Start.After(from) {
			sort.Slice(collected, func(i, j int) bool { return collected[i].Start.Before(collected[j].Start) })
			return collected, nil
		}
		cursor = batch[0].Start
	}
	return nil, errors.New("minute range needs too many pages")
}

// FloorMinute truncates to the minute in UTC.
func FloorMinute(t time.Time) time.Time { return t.UTC().Truncate(time.Minute) }
