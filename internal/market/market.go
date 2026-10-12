// Package market holds the exchange and market identities shared by the
// registry watcher, the candle and notice readers and the tracker.
package market

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type Exchange string

const (
	Upbit   Exchange = "upbit"
	Bithumb Exchange = "bithumb"
)

// Exchanges is the fixed display and processing order.
var Exchanges = []Exchange{Upbit, Bithumb}

func (e Exchange) Valid() bool { return e == Upbit || e == Bithumb }

func (e Exchange) Label() string {
	switch e {
	case Upbit:
		return "업비트"
	case Bithumb:
		return "빗썸"
	}
	return string(e)
}

// Only KRW markets are tracked: their trade amount is already in won, so no
// exchange rate has to be chosen or invented.
const Quote = "KRW"

var MarketListURLs = map[Exchange]string{
	Upbit:   "https://api.upbit.com/v1/market/all?is_details=true",
	Bithumb: "https://api.bithumb.com/v1/market/all?isDetails=true",
}

// ParseAllMarkets reads the same list keeping every quote (KRW, BTC, USDT),
// for the whole market totals.
func ParseAllMarkets(exchange Exchange, payload []byte) ([]Market, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("market list is not a JSON array")
	}
	var rows []listRow
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		return nil, err
	}
	markets := make([]Market, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		code := strings.ToUpper(strings.TrimSpace(row.Market))
		quote, base, ok := strings.Cut(code, "-")
		if !ok || base == "" || quote == "" || seen[code] {
			continue
		}
		seen[code] = true
		markets = append(markets, Market{Exchange: exchange, Code: code, Base: base, Quote: quote})
	}
	return markets, nil
}

type Market struct {
	Exchange    Exchange `json:"exchange"`
	Code        string   `json:"market"`
	Base        string   `json:"base"`
	Quote       string   `json:"quote"`
	KoreanName  string   `json:"korean_name,omitempty"`
	EnglishName string   `json:"english_name,omitempty"`
}

type Key struct {
	Exchange Exchange
	Code     string
}

func (m Market) Key() Key { return Key{Exchange: m.Exchange, Code: m.Code} }

// Code returns the exchange market code for a KRW base asset.
func Code(base string) string { return Quote + "-" + strings.ToUpper(base) }

// Listing is one market that appeared in an exchange's official market list
// after the baseline, or a manually requested watch.
type Listing struct {
	Market
	DetectedAt time.Time `json:"detected_at"`
	// Relisting marks a market that had been removed and came back.
	Relisting bool `json:"relisting"`
	// OnStartup marks a market first seen by the first poll after a restart:
	// it may have been listed while the service was down.
	OnStartup bool   `json:"on_startup"`
	Source    string `json:"source"`
}

const (
	SourceRegistry = "REGISTRY"
	SourceManual   = "MANUAL"
)

type listRow struct {
	Market      string `json:"market"`
	KoreanName  string `json:"korean_name"`
	EnglishName string `json:"english_name"`
}

// ParseMarketList reads an Upbit or Bithumb GET /v1/market/all response and
// returns its KRW markets. A response that is not a JSON array (for example
// an error object) is rejected rather than read as an empty list.
func ParseMarketList(exchange Exchange, payload []byte) ([]Market, error) {
	if !exchange.Valid() {
		return nil, fmt.Errorf("unsupported exchange %q", exchange)
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("market list is not a JSON array")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	var rows []listRow
	if err := decoder.Decode(&rows); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected trailing JSON value")
	}
	seen := make(map[string]bool, len(rows))
	markets := make([]Market, 0, len(rows))
	for _, row := range rows {
		code := strings.ToUpper(strings.TrimSpace(row.Market))
		quote, base, ok := strings.Cut(code, "-")
		if !ok || quote != Quote || base == "" || strings.Contains(base, "-") || seen[code] {
			continue
		}
		seen[code] = true
		markets = append(markets, Market{
			Exchange: exchange, Code: code, Base: base, Quote: quote,
			KoreanName:  strings.TrimSpace(row.KoreanName),
			EnglishName: strings.TrimSpace(row.EnglishName),
		})
	}
	return markets, nil
}
