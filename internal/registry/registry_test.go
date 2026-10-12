package registry

import (
	"testing"
	"time"

	"listing-tracker/internal/market"
)

func markets(codes ...string) []market.Market {
	result := make([]market.Market, 0, len(codes))
	for _, code := range codes {
		result = append(result, market.Market{Exchange: market.Upbit, Code: "KRW-" + code, Base: code, Quote: "KRW"})
	}
	return result
}

func apply(known map[string]KnownMarket, update Update) {
	for _, row := range update.Markets {
		known[row.Code] = row
	}
}

func TestDiffBaselineThenListingAndRelisting(t *testing.T) {
	at := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	known := map[string]KnownMarket{}

	update, err := Diff(market.Upbit, known, markets("BTC", "ETH"), at, true)
	if err != nil || len(update.Listings) != 0 || len(update.Markets) != 2 {
		t.Fatalf("baseline: listings=%d markets=%d err=%v", len(update.Listings), len(update.Markets), err)
	}
	apply(known, update)

	update, err = Diff(market.Upbit, known, markets("BTC", "ETH", "RSR"), at.Add(5*time.Second), false)
	if err != nil || len(update.Listings) != 1 || update.Listings[0].Code != "KRW-RSR" || update.Listings[0].Relisting {
		t.Fatalf("new market: %+v err=%v", update.Listings, err)
	}
	apply(known, update)

	// One missing answer does not remove a market; three do.
	for i := 1; i <= RemoveAfterMissingPolls; i++ {
		update, err = Diff(market.Upbit, known, markets("BTC", "ETH"), at.Add(time.Duration(10+i)*time.Second), false)
		if err != nil {
			t.Fatal(err)
		}
		apply(known, update)
		if active := known["KRW-RSR"].Active; active != (i < RemoveAfterMissingPolls) {
			t.Fatalf("after %d missing polls active=%v", i, active)
		}
	}
	update, err = Diff(market.Upbit, known, markets("BTC", "ETH", "RSR"), at.Add(time.Minute), false)
	if err != nil || len(update.Listings) != 1 || !update.Listings[0].Relisting {
		t.Fatalf("relisting: %+v err=%v", update.Listings, err)
	}
}

func TestDiffMarketThatReturnsWithinGraceIsNotAListing(t *testing.T) {
	at := time.Now()
	known := map[string]KnownMarket{}
	update, _ := Diff(market.Upbit, known, markets("BTC", "ETH", "XRP"), at, true)
	apply(known, update)
	update, _ = Diff(market.Upbit, known, markets("BTC", "ETH"), at.Add(time.Second), false)
	apply(known, update)
	update, _ = Diff(market.Upbit, known, markets("BTC", "ETH", "XRP"), at.Add(2*time.Second), false)
	if len(update.Listings) != 0 {
		t.Fatalf("a market missing from one answer came back as a listing: %+v", update.Listings)
	}
}

func TestDiffRejectsEmptyAndShrunkAnswers(t *testing.T) {
	at := time.Now()
	known := map[string]KnownMarket{}
	update, _ := Diff(market.Upbit, known, markets("A", "B", "C", "D"), at, true)
	apply(known, update)
	if _, err := Diff(market.Upbit, known, nil, at, false); err == nil {
		t.Fatal("empty answer accepted")
	}
	if _, err := Diff(market.Upbit, known, markets("A"), at, false); err == nil {
		t.Fatal("answer keeping 1 of 4 markets accepted")
	}
}

func TestFirstPollAfterRestoreMarksOnStartup(t *testing.T) {
	at := time.Now()
	known := map[string]KnownMarket{}
	update, _ := Diff(market.Upbit, known, markets("BTC"), at, true)
	apply(known, update)
	update, _ = Diff(market.Upbit, known, markets("BTC", "NEW"), at, true)
	if len(update.Listings) != 1 || !update.Listings[0].OnStartup {
		t.Fatalf("listing on first poll after restore: %+v", update.Listings)
	}
}
