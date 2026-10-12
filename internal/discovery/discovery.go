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
	// KRW markets trade every day; a longer silence followed by trades
	// inside the window is a relisting.
	relistGap = 3 * 24 * time.Hour
)

type CandleSource interface {
	Days(ctx context.Context, exchange market.Exchange, code string, count int) ([]candles.Candle, error)
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

func (s *Scanner) Lookback() time.Duration { return s.lookback }

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

// Scan checks each market's day candles and reports every market that is
// new within the window. It continues past single-market failures.
func (s *Scanner) Scan(ctx context.Context, exchange market.Exchange, markets []market.Market, onFound func(Found)) {
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
		found, ok, err := s.check(ctx, exchange, m)
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

func (s *Scanner) check(ctx context.Context, exchange market.Exchange, m market.Market) (Found, bool, error) {
	now := s.now()
	cutoff := now.Add(-s.lookback)
	count := int(s.lookback/(24*time.Hour)) + 3
	days, err := s.source.Days(ctx, exchange, m.Code, count)
	if err != nil {
		return Found{}, false, err
	}
	if len(days) == 0 {
		return Found{Market: m}, true, nil
	}
	first, relisting, ok := firstDayInWindow(days, count, cutoff)
	if !ok {
		return Found{}, false, nil
	}
	// The first ten-minute candle of that day narrows the first trade, then
	// the one-minute candles of those ten minutes give it exactly.
	dayEnd := first.Add(24 * time.Hour)
	tens, err := s.source.Minutes(ctx, exchange, m.Code, 10, dayEnd, 24*6)
	if err != nil {
		return Found{}, false, err
	}
	start := first
	for _, c := range tens {
		if !c.Start.Before(first) {
			start = c.Start
			break
		}
	}
	ones, err := s.source.Minutes(ctx, exchange, m.Code, 1, start.Add(10*time.Minute), 10)
	if err != nil {
		return Found{}, false, err
	}
	firstTrade := start
	for _, c := range ones {
		if !c.Start.Before(start) {
			firstTrade = c.Start
			break
		}
	}
	if firstTrade.Before(cutoff) {
		return Found{}, false, nil
	}
	return Found{Market: m, FirstTrade: firstTrade, Relisting: relisting}, true, nil
}

// firstDayInWindow finds the day trading (re)started, if it is recent. days
// is oldest first; fewer than requested means nothing older exists.
func firstDayInWindow(days []candles.Candle, requested int, cutoff time.Time) (time.Time, bool, bool) {
	segment := 0
	relisting := false
	for i := len(days) - 1; i > 0; i-- {
		if days[i].Start.Sub(days[i-1].Start) > relistGap {
			segment, relisting = i, true
			break
		}
	}
	if !relisting && len(days) >= requested {
		return time.Time{}, false, false // trading began before the window
	}
	first := days[segment].Start
	// The day candle starts before the first trade; keep a day of margin.
	if first.Add(24 * time.Hour).Before(cutoff) {
		return time.Time{}, false, false
	}
	return first, relisting, true
}
