package marketshare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/market"
	"listing-tracker/internal/restclient"
)

// Venue is one of the five domestic exchanges in the overall share. Only
// Upbit and Bithumb are also tracked for listings.
type Venue string

const (
	Bithumb Venue = "bithumb"
	Upbit   Venue = "upbit"
	Coinone Venue = "coinone"
	Korbit  Venue = "korbit"
	Gopax   Venue = "gopax"
)

// Venues is the display order, as on the Bithumb domestic share index.
var Venues = []Venue{Bithumb, Upbit, Coinone, Korbit, Gopax}

// Estimated venues publish candles with quantity but no KRW amount. Their
// hourly amount is quantity × estimatePrice; against Korbit's official
// 24-hour quote volume this was within 0.3%.
var Estimated = map[Venue]bool{Korbit: true, Gopax: true}

// Pair is one market of a venue.
type Pair struct {
	Venue Venue
	Code  string // the venue's own symbol
	Base  string
	Quote string // KRW, BTC or USDT
}

func (p Pair) key() string { return string(p.Venue) + "/" + p.Code }

// Hour is one clock hour of a pair: its amount in the pair's quote
// currency and its typical price, used to convert BTC and USDT markets.
type Hour struct {
	Start  time.Time
	Amount float64
	Price  float64
}

type Fetcher interface {
	Get(context.Context, market.Exchange, restclient.Group, string) ([]byte, error)
}

// Source lists a venue's markets and reads their hourly amounts.
type Source struct {
	fetcher Fetcher
	candles *candles.Client
	now     func() time.Time
}

func NewSource(fetcher Fetcher, candleClient *candles.Client, now func() time.Time) *Source {
	return &Source{fetcher: fetcher, candles: candleClient, now: now}
}

func (s *Source) get(ctx context.Context, venue Venue, group restclient.Group, raw string, v any) error {
	body, err := s.fetcher.Get(ctx, market.Exchange(venue), group, raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return decoder.Decode(v)
}

// Markets lists a venue's markets: every quote on Upbit and Bithumb, KRW on
// the other three, as the Bithumb domestic share index counts them.
func (s *Source) Markets(ctx context.Context, venue Venue) ([]Pair, error) {
	switch venue {
	case Upbit, Bithumb:
		exchange := market.Exchange(venue)
		body, err := s.fetcher.Get(ctx, exchange, restclient.GroupMarket, market.MarketListURLs[exchange])
		if err != nil {
			return nil, err
		}
		markets, err := market.ParseAllMarkets(exchange, body)
		if err != nil {
			return nil, err
		}
		pairs := make([]Pair, 0, len(markets))
		for _, m := range markets {
			if m.Quote == "KRW" || m.Quote == "BTC" || m.Quote == "USDT" {
				pairs = append(pairs, Pair{Venue: venue, Code: m.Code, Base: m.Base, Quote: m.Quote})
			}
		}
		return pairs, nil
	case Coinone:
		var root struct {
			Result  string `json:"result"`
			Markets []struct {
				Target string      `json:"target_currency"`
				Status json.Number `json:"trade_status"`
			} `json:"markets"`
		}
		if err := s.get(ctx, venue, restclient.GroupMarket, "https://api.coinone.co.kr/public/v2/markets/KRW", &root); err != nil {
			return nil, err
		}
		if root.Result != "success" {
			return nil, errors.New("coinone market list failed")
		}
		var pairs []Pair
		for _, m := range root.Markets {
			if base := strings.ToUpper(m.Target); base != "" {
				pairs = append(pairs, Pair{Venue: venue, Code: base, Base: base, Quote: "KRW"})
			}
		}
		return pairs, nil
	case Korbit:
		var root struct {
			Success bool `json:"success"`
			Data    []struct {
				Symbol string `json:"symbol"`
				Status string `json:"status"`
			} `json:"data"`
		}
		if err := s.get(ctx, venue, restclient.GroupMarket, "https://api.korbit.co.kr/v2/currencyPairs", &root); err != nil {
			return nil, err
		}
		if !root.Success {
			return nil, errors.New("korbit market list failed")
		}
		var pairs []Pair
		for _, p := range root.Data {
			if p.Status == "launched" && strings.HasSuffix(p.Symbol, "_krw") {
				pairs = append(pairs, Pair{Venue: venue, Code: p.Symbol, Base: strings.ToUpper(strings.TrimSuffix(p.Symbol, "_krw")), Quote: "KRW"})
			}
		}
		return pairs, nil
	case Gopax:
		var rows []struct {
			Name       string `json:"name"`
			BaseAsset  string `json:"baseAsset"`
			QuoteAsset string `json:"quoteAsset"`
		}
		if err := s.get(ctx, venue, restclient.GroupMarket, "https://api.gopax.co.kr/trading-pairs", &rows); err != nil {
			return nil, err
		}
		var pairs []Pair
		for _, p := range rows {
			if p.QuoteAsset == "KRW" && p.Name != "" {
				pairs = append(pairs, Pair{Venue: venue, Code: p.Name, Base: p.BaseAsset, Quote: "KRW"})
			}
		}
		return pairs, nil
	}
	return nil, fmt.Errorf("unknown venue %q", venue)
}

// Hours reads the latest count clock hours of a pair, the open one included.
func (s *Source) Hours(ctx context.Context, p Pair, count int) ([]Hour, error) {
	switch p.Venue {
	case Upbit, Bithumb:
		rows, err := s.candles.Minutes(ctx, market.Exchange(p.Venue), p.Code, 60, time.Time{}, count)
		if err != nil {
			return nil, err
		}
		return FromCandles(rows), nil
	case Coinone:
		var root struct {
			Result string `json:"result"`
			Chart  []struct {
				Timestamp json.Number `json:"timestamp"`
				High      json.Number `json:"high"`
				Low       json.Number `json:"low"`
				Close     json.Number `json:"close"`
				Quote     json.Number `json:"quote_volume"`
			} `json:"chart"`
		}
		raw := "https://api.coinone.co.kr/public/v2/chart/KRW/" + url.PathEscape(p.Code) + "?interval=1h&size=" + strconv.Itoa(count)
		if err := s.get(ctx, p.Venue, restclient.GroupCandle, raw, &root); err != nil {
			return nil, err
		}
		if root.Result != "success" {
			return nil, errors.New("coinone chart failed")
		}
		hours := make([]Hour, 0, len(root.Chart))
		for _, c := range root.Chart {
			ms, err := c.Timestamp.Int64()
			if err != nil {
				return nil, err
			}
			amount, err := number(c.Quote)
			if err != nil {
				return nil, err
			}
			hours = append(hours, Hour{Start: time.UnixMilli(ms).UTC(), Amount: amount, Price: typical(c.High, c.Low, c.Close)})
		}
		return hours, nil
	case Korbit:
		var root struct {
			Success bool `json:"success"`
			Data    []struct {
				Timestamp json.Number `json:"timestamp"`
				High      json.Number `json:"high"`
				Low       json.Number `json:"low"`
				Close     json.Number `json:"close"`
				Volume    json.Number `json:"volume"`
			} `json:"data"`
		}
		raw := "https://api.korbit.co.kr/v2/candles?symbol=" + url.QueryEscape(p.Code) + "&interval=60&limit=" + strconv.Itoa(count)
		if err := s.get(ctx, p.Venue, restclient.GroupCandle, raw, &root); err != nil {
			return nil, err
		}
		if !root.Success {
			return nil, errors.New("korbit candles failed")
		}
		hours := make([]Hour, 0, len(root.Data))
		for _, c := range root.Data {
			ms, err := c.Timestamp.Int64()
			if err != nil {
				return nil, err
			}
			volume, err := number(c.Volume)
			if err != nil {
				return nil, err
			}
			price := estimatePrice(c.High, c.Low, c.Close)
			hours = append(hours, Hour{Start: time.UnixMilli(ms).UTC(), Amount: volume * price, Price: price})
		}
		return hours, nil
	case Gopax:
		end := s.now()
		start := end.Truncate(time.Hour).Add(-time.Duration(count-1) * time.Hour)
		raw := fmt.Sprintf("https://api.gopax.co.kr/trading-pairs/%s/candles?start=%d&end=%d&interval=30&limit=1024",
			url.PathEscape(p.Code), start.UnixMilli(), end.UnixMilli())
		var rows [][]json.Number
		if err := s.get(ctx, p.Venue, restclient.GroupCandle, raw, &rows); err != nil {
			return nil, err
		}
		// Two 30-minute candles make one hour: [time, low, high, open, close, volume].
		byHour := map[int64]*Hour{}
		for _, c := range rows {
			if len(c) != 6 {
				return nil, errors.New("bad gopax candle")
			}
			ms, err := c[0].Int64()
			if err != nil {
				return nil, err
			}
			volume, err := number(c[5])
			if err != nil {
				return nil, err
			}
			price := estimatePrice(c[2], c[1], c[4])
			hourStart := time.UnixMilli(ms).UTC().Truncate(time.Hour)
			h := byHour[hourStart.Unix()]
			if h == nil {
				h = &Hour{Start: hourStart}
				byHour[hourStart.Unix()] = h
			}
			h.Amount += volume * price
			h.Price = price
		}
		hours := make([]Hour, 0, len(byHour))
		for _, h := range byHour {
			hours = append(hours, *h)
		}
		return hours, nil
	}
	return nil, fmt.Errorf("unknown venue %q", p.Venue)
}

// FromCandles turns Upbit or Bithumb 60-minute candles into hours.
func FromCandles(rows []candles.Candle) []Hour {
	hours := make([]Hour, 0, len(rows))
	for _, c := range rows {
		price := c.Close
		if c.High > 0 && c.Low > 0 {
			price = (c.High + c.Low + c.Close) / 3
		}
		hours = append(hours, Hour{Start: c.Start, Amount: c.Amount, Price: price})
	}
	return hours
}

func number(value json.Number) (float64, error) {
	v, err := strconv.ParseFloat(string(value), 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid number %q", value)
	}
	return v, nil
}

// estimatePrice is the price an estimated amount uses: the hour's typical
// price, or its close when the hour holds an outlier trade (high above 1.5
// times low). A Korbit listing's first trade at 10,000,000 against a close
// of 174 otherwise turned 2억 of trading into 9,315억.
func estimatePrice(high, low, close json.Number) float64 {
	h, _ := strconv.ParseFloat(string(high), 64)
	l, _ := strconv.ParseFloat(string(low), 64)
	c, _ := strconv.ParseFloat(string(close), 64)
	if h > 0 && l > 0 && h <= l*1.5 {
		return (h + l + c) / 3
	}
	return c
}

func typical(high, low, close json.Number) float64 {
	h, _ := strconv.ParseFloat(string(high), 64)
	l, _ := strconv.ParseFloat(string(low), 64)
	c, _ := strconv.ParseFloat(string(close), 64)
	if h > 0 && l > 0 {
		return (h + l + c) / 3
	}
	return c
}
