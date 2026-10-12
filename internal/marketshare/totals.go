// Package marketshare keeps each exchange's whole KRW market trading amount
// per clock hour, summed over every KRW market from official 60-minute
// candles, so a listing can be compared with Bithumb's overall share before
// and after it.
package marketshare

import (
	"context"
	"sync"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/market"
)

const (
	// keepHours covers the lookback window plus the day before a listing.
	keepHours = 240
	// HoursPerRead is the startup read: 200 hours, a bit over eight days.
	HoursPerRead = 200
)

// Status of a window sum. A missing hour is never an observed 0.
const (
	StatusFinal   = "FINAL"
	StatusPartial = "PARTIAL"
	StatusWaiting = "WAITING"
)

type Totals struct {
	mu       sync.Mutex
	byMarket map[market.Key]map[int64]float64 // hour -> amount
	sum      map[market.Exchange]map[int64]float64
	ready    map[market.Exchange]bool
	since    map[market.Exchange]int64 // first hour every market covers
	failed   map[market.Exchange]int
	cycleAt  map[market.Exchange]time.Time
}

func New() *Totals {
	t := &Totals{
		byMarket: map[market.Key]map[int64]float64{},
		sum:      map[market.Exchange]map[int64]float64{},
		ready:    map[market.Exchange]bool{},
		since:    map[market.Exchange]int64{},
		failed:   map[market.Exchange]int{},
		cycleAt:  map[market.Exchange]time.Time{},
	}
	for _, exchange := range market.Exchanges {
		t.sum[exchange] = map[int64]float64{}
	}
	return t
}

func hourOf(t time.Time) int64 { return t.Unix() / 3600 }

// Apply stores one market's hourly candles, replacing those hours.
func (t *Totals) Apply(key market.Key, hours []candles.Candle) {
	t.mu.Lock()
	defer t.mu.Unlock()
	values := t.byMarket[key]
	if values == nil {
		values = map[int64]float64{}
		t.byMarket[key] = values
	}
	sums := t.sum[key.Exchange]
	if sums == nil {
		return
	}
	newest := int64(0)
	for _, c := range hours {
		hour := hourOf(c.Start)
		sums[hour] += c.Amount - values[hour]
		values[hour] = c.Amount
		newest = max(newest, hour)
	}
	if newest == 0 {
		return
	}
	for hour := range values {
		if hour < newest-keepHours {
			sums[hour] -= values[hour]
			delete(values, hour)
		}
	}
}

// MarkReady records that every market of the exchange was read once from
// sinceHour on; failed markets make the sums partial.
func (t *Totals) MarkReady(exchange market.Exchange, sinceHour int64, failed int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ready[exchange], t.since[exchange], t.failed[exchange] = true, sinceHour, failed
}

// Window sums hours [from, to] of both exchanges.
func (t *Totals) Window(from, to int64) (bithumb, upbit float64, status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	status = StatusFinal
	for _, exchange := range market.Exchanges {
		if !t.ready[exchange] {
			return 0, 0, StatusWaiting
		}
		if from < t.since[exchange] || t.failed[exchange] > 0 {
			status = StatusPartial
		}
	}
	for hour := from; hour <= to; hour++ {
		bithumb += t.sum[market.Bithumb][hour]
		upbit += t.sum[market.Upbit][hour]
	}
	return bithumb, upbit, status
}

type Status struct {
	Ready     bool      `json:"ready"`
	Since     int64     `json:"since"`
	Failed    int       `json:"failed"`
	LastCycle time.Time `json:"last_cycle"`
}

func (t *Totals) Statuses() map[market.Exchange]Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := map[market.Exchange]Status{}
	for _, exchange := range market.Exchanges {
		result[exchange] = Status{Ready: t.ready[exchange], Since: t.since[exchange] * 3600 * 1000, Failed: t.failed[exchange], LastCycle: t.cycleAt[exchange]}
	}
	return result
}

type CandleSource interface {
	Minutes(ctx context.Context, exchange market.Exchange, code string, unit int, to time.Time, count int) ([]candles.Candle, error)
}

// Refresh re-reads every KRW market's latest three hourly candles in turn,
// perSecond requests a second, so a closed hour is final within one cycle
// and the open hour stays a few minutes fresh.
func (t *Totals) Refresh(ctx context.Context, source CandleSource, exchange market.Exchange, markets func() []market.Market, perSecond float64) {
	gap := time.Duration(float64(time.Second) / perSecond)
	for ctx.Err() == nil {
		list := markets()
		if len(list) == 0 {
			if !sleep(ctx, time.Second) {
				return
			}
			continue
		}
		for _, m := range list {
			hours, err := source.Minutes(ctx, exchange, m.Code, 60, time.Time{}, 3)
			if err == nil {
				t.Apply(m.Key(), hours)
			}
			if !sleep(ctx, gap) {
				return
			}
		}
		t.mu.Lock()
		t.cycleAt[exchange] = time.Now()
		t.mu.Unlock()
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
