package marketshare

import (
	"testing"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/market"
)

func hours(from time.Time, amounts ...float64) []candles.Candle {
	result := make([]candles.Candle, len(amounts))
	for i, a := range amounts {
		result[i] = candles.Candle{Start: from.Add(time.Duration(i) * time.Hour), Amount: a}
	}
	return result
}

func TestWindowSumsEveryMarketAndReplacesRereadHours(t *testing.T) {
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	h := hourOf(start)
	totals := New()
	if _, _, status := totals.Window(h, h+1); status != StatusWaiting {
		t.Fatalf("before any read: %s", status)
	}
	totals.Apply(market.Key{Exchange: market.Bithumb, Code: "KRW-A"}, hours(start, 10, 20))
	totals.Apply(market.Key{Exchange: market.Bithumb, Code: "KRW-B"}, hours(start, 5, 5))
	totals.Apply(market.Key{Exchange: market.Upbit, Code: "KRW-A"}, hours(start, 30, 40))
	totals.MarkReady(market.Bithumb, h, 0)
	totals.MarkReady(market.Upbit, h, 0)
	if b, u, status := totals.Window(h, h+1); b != 40 || u != 70 || status != StatusFinal {
		t.Fatalf("window b=%v u=%v %s", b, u, status)
	}
	// A refresh replaces the hour instead of adding to it.
	totals.Apply(market.Key{Exchange: market.Bithumb, Code: "KRW-A"}, hours(start.Add(time.Hour), 25))
	if b, _, _ := totals.Window(h+1, h+1); b != 30 {
		t.Fatalf("re-read hour b=%v", b)
	}
	if _, _, status := totals.Window(h-1, h); status != StatusPartial {
		t.Fatalf("window before the covered hours: %s", status)
	}
}
