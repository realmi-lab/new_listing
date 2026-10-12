package discovery

import (
	"context"
	"sort"
	"testing"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/market"
)

// fakeSource has trades on the given minutes; day and ten-minute candles are
// built from them like an exchange would.
type fakeSource struct{ minutes map[string][]time.Time }

func (f fakeSource) grouped(code string, size time.Duration, to time.Time, count int) []candles.Candle {
	buckets := map[time.Time]bool{}
	for _, m := range f.minutes[code] {
		if to.IsZero() || m.Before(to) {
			buckets[m.Truncate(size)] = true
		}
	}
	starts := make([]time.Time, 0, len(buckets))
	for s := range buckets {
		if to.IsZero() || s.Before(to) {
			starts = append(starts, s)
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	if len(starts) > count {
		starts = starts[len(starts)-count:]
	}
	result := make([]candles.Candle, len(starts))
	for i, s := range starts {
		result[i] = candles.Candle{Start: s, Amount: 1}
	}
	return result
}

func (f fakeSource) Days(_ context.Context, _ market.Exchange, code string, count int) ([]candles.Candle, error) {
	return f.grouped(code, 24*time.Hour, time.Time{}, count), nil
}

func (f fakeSource) Minutes(_ context.Context, _ market.Exchange, code string, unit int, to time.Time, count int) ([]candles.Candle, error) {
	return f.grouped(code, time.Duration(unit)*time.Minute, to, count), nil
}

func minutes(from time.Time, n int) []time.Time {
	result := make([]time.Time, n)
	for i := range result {
		result[i] = from.Add(time.Duration(i) * time.Minute)
	}
	return result
}

func TestScanFindsFirstTradeMinuteOfRecentListings(t *testing.T) {
	now := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)
	listed := time.Date(2026, 10, 9, 8, 17, 0, 0, time.UTC)
	relisted := time.Date(2026, 10, 11, 1, 2, 0, 0, time.UTC)
	source := fakeSource{minutes: map[string][]time.Time{
		"KRW-NEW":  minutes(listed, 3*24*60),
		"KRW-OLD":  minutes(now.Add(-60*24*time.Hour), 59*24*60),
		"KRW-BACK": append(minutes(now.Add(-60*24*time.Hour), 24*60), minutes(relisted, 600)...),
		"KRW-SOON": nil,
		"KRW-AGED": minutes(now.Add(-9*24*time.Hour), 24*60),
	}}
	scanner := New(source, 7*24*time.Hour)
	scanner.SetClock(func() time.Time { return now })
	var all []market.Market
	for _, code := range []string{"NEW", "OLD", "BACK", "SOON", "AGED"} {
		all = append(all, market.Market{Exchange: market.Upbit, Code: "KRW-" + code, Base: code, Quote: "KRW"})
	}
	found := map[string]Found{}
	scanner.Scan(context.Background(), market.Upbit, all, func(f Found) { found[f.Market.Base] = f })

	if f, ok := found["NEW"]; !ok || !f.FirstTrade.Equal(listed) || f.Relisting {
		t.Fatalf("NEW: %+v ok=%v", f, ok)
	}
	if f, ok := found["BACK"]; !ok || !f.FirstTrade.Equal(relisted) || !f.Relisting {
		t.Fatalf("BACK: %+v ok=%v", f, ok)
	}
	if f, ok := found["SOON"]; !ok || !f.FirstTrade.IsZero() {
		t.Fatalf("SOON: %+v ok=%v", f, ok)
	}
	for _, base := range []string{"OLD", "AGED"} {
		if _, ok := found[base]; ok {
			t.Fatalf("%s reported as new", base)
		}
	}
	if p := scanner.Progress()[market.Upbit]; p.Done != 5 || p.Found != 3 || p.Running {
		t.Fatalf("progress %+v", p)
	}
}
