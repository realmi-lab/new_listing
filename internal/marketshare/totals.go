// Package marketshare keeps the five domestic exchanges' whole market
// trading amount per clock hour, from official 60-minute candles (Gopax:
// 30-minute), so a listing can be compared with Bithumb's overall share
// before and after it, on the basis of the Bithumb domestic share index.
package marketshare

import (
	"context"
	"sync"
	"time"
)

const (
	// keepHours covers the lookback window plus the day before a listing.
	keepHours = 240
	// HoursPerRead is the startup read: 200 hours, a bit over eight days.
	HoursPerRead = 200
	// listEvery is how often a venue's market list is read again.
	listEvery = 30 * time.Minute
)

// Status of a window sum. A missing hour is never an observed 0.
const (
	StatusFinal   = "FINAL"
	StatusPartial = "PARTIAL"
	StatusWaiting = "WAITING"
)

type Totals struct {
	mu      sync.Mutex
	byPair  map[string]map[int64]float64           // pair -> hour -> amount in its quote
	sum     map[Venue]map[string]map[int64]float64 // venue -> quote -> hour -> amount
	fx      map[Venue]map[string]map[int64]float64 // venue -> BTC/USDT -> hour -> KRW price
	parts   map[Venue]int
	ready   map[Venue]bool
	since   map[Venue]int64
	failed  map[Venue]int
	cycleAt map[Venue]time.Time
}

func New() *Totals {
	t := &Totals{
		byPair: map[string]map[int64]float64{}, sum: map[Venue]map[string]map[int64]float64{},
		fx: map[Venue]map[string]map[int64]float64{}, parts: map[Venue]int{}, ready: map[Venue]bool{},
		since: map[Venue]int64{}, failed: map[Venue]int{}, cycleAt: map[Venue]time.Time{},
	}
	for _, v := range Venues {
		t.sum[v], t.fx[v], t.parts[v] = map[string]map[int64]float64{}, map[string]map[int64]float64{}, 1
	}
	return t
}

func hourOf(t time.Time) int64 { return t.Unix() / 3600 }

// Expect sets how many startup reads a venue needs before it is ready.
func (t *Totals) Expect(v Venue, parts int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.parts[v] = parts
}

// Apply stores a pair's hours, replacing those hours. A KRW-BTC or KRW-USDT
// pair also gives that venue's hourly price for converting BTC and USDT
// markets.
func (t *Totals) Apply(p Pair, hours []Hour) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sums := t.sum[p.Venue]
	if sums == nil {
		return
	}
	values := t.byPair[p.key()]
	if values == nil {
		values = map[int64]float64{}
		t.byPair[p.key()] = values
	}
	bucket := sums[p.Quote]
	if bucket == nil {
		bucket = map[int64]float64{}
		sums[p.Quote] = bucket
	}
	var prices map[int64]float64
	if p.Quote == "KRW" && (p.Base == "BTC" || p.Base == "USDT") {
		prices = t.fx[p.Venue][p.Base]
		if prices == nil {
			prices = map[int64]float64{}
			t.fx[p.Venue][p.Base] = prices
		}
	}
	newest := int64(0)
	for _, h := range hours {
		hour := hourOf(h.Start)
		bucket[hour] += h.Amount - values[hour]
		values[hour] = h.Amount
		if prices != nil && h.Price > 0 {
			prices[hour] = h.Price
		}
		newest = max(newest, hour)
	}
	if newest == 0 {
		return
	}
	for hour := range values {
		if hour < newest-keepHours {
			bucket[hour] -= values[hour]
			delete(values, hour)
		}
	}
}

// PartDone records one finished startup read of a venue: every market from
// sinceHour on, with failed markets making its sums partial. The venue is
// shown after its first read (Upbit and Bithumb: the KRW markets); until
// the rest is read its sums are partial.
func (t *Totals) PartDone(v Venue, sinceHour int64, failed int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.parts[v]--
	t.since[v] = max(t.since[v], sinceHour)
	t.failed[v] += failed
	t.ready[v] = true
}

// WindowSum is each venue's KRW amount over clock hours [from, to].
type WindowSum struct {
	Venues map[Venue]float64
	Total  float64
	Status string
}

// Window sums hours [from, to]. It waits for Bithumb and Upbit; another
// venue that is missing or failed only makes the sum partial.
func (t *Totals) Window(from, to int64) WindowSum {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := WindowSum{Venues: map[Venue]float64{}, Status: StatusFinal}
	for _, v := range Venues {
		if !t.ready[v] {
			if v == Bithumb || v == Upbit {
				return WindowSum{Status: StatusWaiting}
			}
			result.Status = StatusPartial
			continue
		}
		if from < t.since[v] || t.failed[v] > 0 || t.parts[v] > 0 {
			result.Status = StatusPartial
		}
		amount := 0.0
		for hour := from; hour <= to; hour++ {
			amount += t.sum[v]["KRW"][hour]
			for _, quote := range []string{"BTC", "USDT"} {
				a := t.sum[v][quote][hour]
				if a <= 0 {
					continue
				}
				if price := t.price(v, quote, hour); price > 0 {
					amount += a * price
				} else {
					result.Status = StatusPartial
				}
			}
		}
		result.Venues[v] = amount
		result.Total += amount
	}
	return result
}

// price is the venue's KRW price of quote in that hour, or of the latest of
// the three hours before it: a BTC market hour can arrive before the
// KRW-BTC hour that converts it.
func (t *Totals) price(v Venue, quote string, hour int64) float64 {
	for h := hour; h >= hour-3; h-- {
		if p := t.fx[v][quote][h]; p > 0 {
			return p
		}
	}
	return 0
}

type Status struct {
	Ready     bool      `json:"ready"`
	Since     int64     `json:"since"`
	Failed    int       `json:"failed"`
	Estimated bool      `json:"estimated"`
	LastCycle time.Time `json:"last_cycle"`
}

func (t *Totals) Statuses() map[Venue]Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := map[Venue]Status{}
	for _, v := range Venues {
		result[v] = Status{Ready: t.ready[v], Since: t.since[v] * 3600 * 1000, Failed: t.failed[v], Estimated: Estimated[v], LastCycle: t.cycleAt[v]}
	}
	return result
}

// Prime reads HoursPerRead hours of every market of a venue once. skip leaves
// out markets another reader already covers (Upbit and Bithumb KRW markets
// come with the listing scan).
func (t *Totals) Prime(ctx context.Context, source *Source, v Venue, skip func(Pair) bool) {
	start := source.now()
	pairs, err := source.Markets(ctx, v)
	failed := 0
	if err != nil {
		failed = 1
	}
	for _, p := range pairs {
		if ctx.Err() != nil {
			return
		}
		if skip != nil && skip(p) {
			continue
		}
		hours, err := source.Hours(ctx, p, HoursPerRead)
		if err != nil {
			failed++
			continue
		}
		t.Apply(p, hours)
	}
	t.PartDone(v, hourOf(start)-HoursPerRead+1, failed)
}

// Refresh re-reads every market's latest three hours in turn, perSecond
// requests a second, so a closed hour is final within one cycle and the
// open hour stays a few minutes fresh.
func (t *Totals) Refresh(ctx context.Context, source *Source, v Venue, perSecond float64) {
	gap := time.Duration(float64(time.Second) / perSecond)
	var pairs []Pair
	var listedAt time.Time
	for ctx.Err() == nil {
		if pairs == nil || time.Since(listedAt) > listEvery {
			if list, err := source.Markets(ctx, v); err == nil {
				pairs, listedAt = list, time.Now()
			} else if !sleep(ctx, 10*time.Second) {
				return
			}
			if pairs == nil {
				continue
			}
		}
		for _, p := range pairs {
			if hours, err := source.Hours(ctx, p, 3); err == nil {
				t.Apply(p, hours)
			}
			if !sleep(ctx, gap) {
				return
			}
		}
		t.mu.Lock()
		t.cycleAt[v] = time.Now()
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
