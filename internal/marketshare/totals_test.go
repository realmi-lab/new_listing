package marketshare

import (
	"testing"
	"time"
)

func hours(from time.Time, amounts ...float64) []Hour {
	result := make([]Hour, len(amounts))
	for i, a := range amounts {
		result[i] = Hour{Start: from.Add(time.Duration(i) * time.Hour), Amount: a, Price: 100}
	}
	return result
}

func readyAll(t *Totals, since int64) {
	for _, v := range Venues {
		t.Expect(v, 1)
		t.PartDone(v, since, 0)
	}
}

func TestWindowSumsFiveVenuesAndConvertsBTCMarkets(t *testing.T) {
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	h := hourOf(start)
	totals := New()
	if w := totals.Window(h, h+1); w.Status != StatusWaiting {
		t.Fatalf("before any read: %s", w.Status)
	}
	totals.Apply(Pair{Venue: Bithumb, Code: "KRW-A", Base: "A", Quote: "KRW"}, hours(start, 10, 20))
	totals.Apply(Pair{Venue: Upbit, Code: "KRW-A", Base: "A", Quote: "KRW"}, hours(start, 30, 40))
	// Upbit's KRW-BTC price (100 in the helper) converts its BTC market.
	totals.Apply(Pair{Venue: Upbit, Code: "KRW-BTC", Base: "BTC", Quote: "KRW"}, hours(start, 0, 0))
	totals.Apply(Pair{Venue: Upbit, Code: "BTC-X", Base: "X", Quote: "BTC"}, hours(start, 0.5, 0))
	totals.Apply(Pair{Venue: Coinone, Code: "A", Base: "A", Quote: "KRW"}, hours(start, 5, 5))
	totals.Apply(Pair{Venue: Korbit, Code: "a_krw", Base: "A", Quote: "KRW"}, hours(start, 2, 2))
	readyAll(totals, h)
	w := totals.Window(h, h+1)
	if w.Status != StatusFinal || w.Venues[Bithumb] != 30 || w.Venues[Upbit] != 70+50 || w.Total != 30+120+10+4 {
		t.Fatalf("window %+v", w)
	}
	// A refresh replaces the hour instead of adding to it.
	totals.Apply(Pair{Venue: Bithumb, Code: "KRW-A", Base: "A", Quote: "KRW"}, hours(start.Add(time.Hour), 25))
	if w := totals.Window(h+1, h+1); w.Venues[Bithumb] != 25 {
		t.Fatalf("re-read hour %+v", w)
	}
	if w := totals.Window(h-1, h); w.Status != StatusPartial {
		t.Fatalf("window before the covered hours: %s", w.Status)
	}
}

func TestVenueIsShownAfterItsFirstReadAndPartialUntilTheRest(t *testing.T) {
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	h := hourOf(start)
	totals := New()
	readyAll(totals, h)
	totals.Expect(Upbit, 2)
	totals.PartDone(Upbit, h, 0) // KRW markets read, BTC and USDT not yet
	if w := totals.Window(h, h); w.Status != StatusPartial {
		t.Fatalf("after the KRW read: %s", w.Status)
	}
	totals.PartDone(Upbit, h, 0)
	if w := totals.Window(h, h); w.Status != StatusFinal {
		t.Fatalf("after both reads: %s", w.Status)
	}
}

func TestMissingSmallVenueMakesTheSumPartialNotWaiting(t *testing.T) {
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	h := hourOf(start)
	totals := New()
	for _, v := range []Venue{Bithumb, Upbit, Coinone, Korbit} {
		totals.PartDone(v, h, 0)
	}
	if w := totals.Window(h, h); w.Status != StatusPartial {
		t.Fatalf("gopax not read yet: %s", w.Status)
	}
}
