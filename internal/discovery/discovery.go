// Package discovery finds, at startup, the KRW markets whose first trade is
// within the lookback window, so a late start still shows each listing from
// its first minute.
package discovery

import (
	"context"
	"sync"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/market"
)

const (
	DefaultLookback = 7 * 24 * time.Hour
	// HoursPerRead is one read of 60-minute candles, a bit over eight days:
	// the lookback window plus the day before a listing.
	HoursPerRead = 200
	// MaxLookback keeps the first trading hour and the day before it
	// inside one read.
	MaxLookback = 8 * 24 * time.Hour
	// KRW markets trade every day; a longer silence followed by trades
	// inside the window is a relisting.
	relistGap = 3 * 24 * time.Hour
)

type CandleSource interface {
	Minutes(ctx context.Context, exchange market.Exchange, code string, unit int, to time.Time, count int) ([]candles.Candle, error)
}

// Found is one market whose trading began in the window, or has not begun.
type Found struct {
	Market market.Market
	// FirstTrade is the start of the first one-minute candle; zero when the
	// market has no candle yet (listed, trading not started).
	FirstTrade time.Time
	Relisting  bool
}

type Progress struct {
	Total   int    `json:"total"`
	Done    int    `json:"done"`
	Found   int    `json:"found"`
	Errors  int    `json:"errors"`
	Running bool   `json:"running"`
	Error   string `json:"last_error,omitempty"`
}

type Scanner struct {
	source   CandleSource
	lookback time.Duration
	now      func() time.Time

	mu       sync.Mutex
	progress map[market.Exchange]*Progress
}

func New(source CandleSource, lookback time.Duration) *Scanner {
	if lookback <= 0 {
		lookback = DefaultLookback
	}
	s := &Scanner{source: source, lookback: lookback, now: time.Now, progress: map[market.Exchange]*Progress{}}
	for _, exchange := range market.Exchanges {
		s.progress[exchange] = &Progress{}
	}
	return s
}

// SetClock replaces the clock, normally with one corrected to the exchanges.
func (s *Scanner) SetClock(now func() time.Time) { s.now = now }

func (s *Scanner) Progress() map[market.Exchange]Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[market.Exchange]Progress{}
	for exchange, p := range s.progress {
		result[exchange] = *p
	}
	return result
}

// Scan reads each market's hourly candles, hands them to onHours (the whole
// market totals) and reports every market that is new within the window.
// It continues past single-market failures.
func (s *Scanner) Scan(ctx context.Context, exchange market.Exchange, markets []market.Market, onFound func(Found), onHours func(market.Market, []candles.Candle)) {
	s.mu.Lock()
	*s.progress[exchange] = Progress{Total: len(markets), Running: true}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.progress[exchange].Running = false
		s.mu.Unlock()
	}()
	for _, m := range markets {
		if ctx.Err() != nil {
			return
		}
		found, ok, err := s.check(ctx, exchange, m, onHours)
		s.mu.Lock()
		p := s.progress[exchange]
		p.Done++
		if err != nil {
			p.Errors++
			p.Error = m.Code + ": " + err.Error()
		} else if ok {
			p.Found++
		}
		s.mu.Unlock()
		if ok && onFound != nil {
			onFound(found)
		}
	}
}

func (s *Scanner) check(ctx context.Context, exchange market.Exchange, m market.Market, onHours func(market.Market, []candles.Candle)) (Found, bool, error) {
	now := s.now()
	cutoff := now.Add(-s.lookback)
	hours, err := s.source.Minutes(ctx, exchange, m.Code, 60, time.Time{}, HoursPerRead)
	if err != nil {
		return Found{}, false, err
	}
	if onHours != nil {
		onHours(m, hours)
	}
	if len(hours) == 0 {
		return Found{Market: m}, true, nil
	}
	first, relisting, ok := firstHourInWindow(hours, HoursPerRead, cutoff)
	if !ok {
		return Found{}, false, nil
	}
	// The one-minute candles of that first hour give the first trade minute.
	ones, err := s.source.Minutes(ctx, exchange, m.Code, 1, first.Add(time.Hour), 60)
	if err != nil {
		return Found{}, false, err
	}
	firstTrade := first
	for _, c := range ones {
		if !c.Start.Before(first) {
			firstTrade = c.Start
			break
		}
	}
	if firstTrade.Before(cutoff) {
		return Found{}, false, nil
	}
	return Found{Market: m, FirstTrade: firstTrade, Relisting: relisting}, true, nil
}

// firstHourInWindow finds the hour trading (re)started, if it is recent.
// hours is oldest first; fewer than requested means nothing older exists.
func firstHourInWindow(hours []candles.Candle, requested int, cutoff time.Time) (time.Time, bool, bool) {
	segment := 0
	relisting := false
	for i := len(hours) - 1; i > 0; i-- {
		if hours[i].Start.Sub(hours[i-1].Start) > relistGap {
			segment, relisting = i, true
			break
		}
	}
	if !relisting && len(hours) >= requested {
		return time.Time{}, false, false // trading began before the window
	}
	first := hours[segment].Start
	if first.Add(time.Hour).Before(cutoff) {
		return time.Time{}, false, false
	}
	return first, relisting, true
}
