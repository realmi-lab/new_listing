package tracker

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/market"
)

// fakeSource serves one-minute candles that exist up to the fake clock;
// the minute in progress is included as an open candle.
type fakeSource struct {
	now     *time.Time
	candles map[market.Key][]candles.Candle
	fail    bool
}

func (f *fakeSource) visible(key market.Key) []candles.Candle {
	var result []candles.Candle
	for _, c := range f.candles[key] {
		if !c.Start.After(*f.now) {
			result = append(result, c)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Start.Before(result[j].Start) })
	return result
}

func (f *fakeSource) Minutes(_ context.Context, exchange market.Exchange, code string, _ int, to time.Time, count int) ([]candles.Candle, error) {
	if f.fail {
		return nil, errors.New("exchange unavailable")
	}
	all := f.visible(market.Key{Exchange: exchange, Code: code})
	var result []candles.Candle
	for i := len(all) - 1; i >= 0 && len(result) < count; i-- {
		if to.IsZero() || all[i].Start.Before(to) {
			result = append([]candles.Candle{all[i]}, result...)
		}
	}
	return result, nil
}

func (f *fakeSource) MinuteRange(_ context.Context, exchange market.Exchange, code string, from, to time.Time) ([]candles.Candle, error) {
	if f.fail {
		return nil, errors.New("exchange unavailable")
	}
	var result []candles.Candle
	for _, c := range f.visible(market.Key{Exchange: exchange, Code: code}) {
		if !c.Start.Before(from) && c.Start.Before(to) {
			result = append(result, c)
		}
	}
	return result, nil
}

type fakeLookup map[string][]market.Market

func (l fakeLookup) ActiveMarketsForBase(base string) []market.Market { return l[base] }

func mk(exchange market.Exchange, base string) market.Market {
	return market.Market{Exchange: exchange, Code: "KRW-" + base, Base: base, Quote: "KRW"}
}

// series makes one candle per minute in [from, from+n) with amount(i).
func series(from time.Time, n int, amount func(i int) float64) []candles.Candle {
	result := make([]candles.Candle, 0, n)
	for i := 0; i < n; i++ {
		if a := amount(i); a >= 0 {
			result = append(result, candles.Candle{Start: from.Add(time.Duration(i) * time.Minute), Amount: a, Close: 1, LastTradeAt: from.Add(time.Duration(i)*time.Minute + 30*time.Second)})
		}
	}
	return result
}

func drain(t *testing.T, tr *Tracker, source CandleSource, now time.Time) {
	t.Helper()
	for _, exchange := range market.Exchanges {
		for i := 0; ; i++ {
			if i > 200 {
				t.Fatalf("%s: jobs did not settle", exchange)
			}
			j, _, ok := tr.nextJob(exchange, now)
			if !ok {
				break
			}
			result, err := tr.execute(context.Background(), source, exchange, j)
			tr.apply(j, result, err, now)
		}
	}
}

func amount(c Cell) float64 {
	if c.Amount == nil {
		return -1
	}
	return *c.Amount
}

func TestLateStartRebuildsTheFirst24HoursFromCandles(t *testing.T) {
	first := time.Date(2026, 10, 10, 8, 3, 0, 0, time.UTC) // hours start at :03
	now := first.Add(30 * time.Hour)
	upbit, bithumb := mk(market.Upbit, "NEW"), mk(market.Bithumb, "NEW")
	source := &fakeSource{now: &now, candles: map[market.Key][]candles.Candle{
		// 10 per minute on Upbit, nothing in minutes divisible by 5.
		upbit.Key(): series(first, 40*60, func(i int) float64 {
			if i%5 == 0 {
				return -1
			}
			return 10
		}),
		// Bithumb already listed the coin and trades 1 per minute throughout.
		bithumb.Key(): series(first.Add(-48*time.Hour), 100*60, func(int) float64 { return 1 }),
	}}
	tr := New(fakeLookup{"NEW": {upbit, bithumb}}, 7*24*time.Hour)
	tr.SetClock(func() time.Time { return now })
	tr.AddListing(Listing{Market: upbit, FirstTrade: first, Source: SourceScan})
	drain(t, tr, source, now)

	snap := tr.Snapshot(now)
	if len(snap.Coins) != 1 {
		t.Fatalf("coins=%d", len(snap.Coins))
	}
	coin := snap.Coins[0]
	if coin.State != "COMPLETE" || *coin.AnchorAt != first.UnixMilli() || len(coin.Hours) != TrackHours {
		t.Fatalf("state=%s anchor=%v hours=%d", coin.State, coin.AnchorAt, len(coin.Hours))
	}
	for _, h := range coin.Hours {
		if h.Status != StatusFinal {
			t.Fatalf("hour %d status %s", h.Index, h.Status)
		}
		// 48 Upbit minutes of 10 plus 60 Bithumb minutes of 1.
		if got := amount(h.Total); got != 48*10+60 {
			t.Fatalf("hour %d total %v", h.Index, got)
		}
		if got := amount(h.Exchanges["upbit"]); got != 480 {
			t.Fatalf("hour %d upbit %v", h.Index, got)
		}
	}
	if got := amount(coin.RecentHour); got != 540 || coin.RecentHour.Status != StatusFinal {
		t.Fatalf("recent hour after 24 hours = %v %s", got, coin.RecentHour.Status)
	}
	if up, bt := amount(coin.Exchanges["upbit"].RecentHour), amount(coin.Exchanges["bithumb"].RecentHour); up != 480 || bt != 60 {
		t.Fatalf("recent hour by exchange: upbit %v bithumb %v", up, bt)
	}
	if got := *coin.Hours[23].ExchangeCumulative["upbit"]; got != 24*480 {
		t.Fatalf("upbit cumulative %v", got)
	}
	if got := *coin.Hours[23].Cumulative; got != 24*540 {
		t.Fatalf("cumulative %v", got)
	}
	if !coin.Exchanges["upbit"].NewlyListed || coin.Exchanges["bithumb"].NewlyListed {
		t.Fatalf("new flags %+v", coin.Exchanges)
	}
	if share := *coin.Exchanges["upbit"].Cumulative.Share; share < 88.8 || share > 88.9 {
		t.Fatalf("upbit share %v", share)
	}
}

func TestRunningListingShowsFinalLiveAndPendingHours(t *testing.T) {
	first := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)
	now := first.Add(90*time.Minute + 20*time.Second)
	upbit := mk(market.Upbit, "RUN")
	source := &fakeSource{now: &now, candles: map[market.Key][]candles.Candle{
		upbit.Key(): series(first, 24*60, func(i int) float64 { return float64(i + 1) }),
	}}
	tr := New(fakeLookup{"RUN": {upbit}}, 7*24*time.Hour)
	tr.SetClock(func() time.Time { return now })
	tr.AddListing(Listing{Market: upbit, FirstTrade: first, Source: SourceScan})
	drain(t, tr, source, now)

	coin := tr.Snapshot(now).Coins[0]
	if coin.State != "TRACKING" || coin.CurrentHour != 2 {
		t.Fatalf("state=%s hour=%d", coin.State, coin.CurrentHour)
	}
	if coin.Hours[0].Status != StatusFinal || coin.Hours[1].Status != StatusLive || coin.Hours[2].Status != StatusPending {
		t.Fatalf("statuses %s %s %s", coin.Hours[0].Status, coin.Hours[1].Status, coin.Hours[2].Status)
	}
	if got := amount(coin.Hours[0].Total); got != 60*61/2 {
		t.Fatalf("hour 1 = %v", got)
	}
	// Hour 2 so far: minutes 61..91 (the open minute included).
	if got := amount(coin.Hours[1].Total); got != float64(sum(61, 91)) {
		t.Fatalf("hour 2 = %v want %d", got, sum(61, 91))
	}
	// The last 60 minutes: minutes 32..91.
	if got := amount(coin.RecentHour); got != float64(sum(32, 91)) || coin.RecentHour.Status != StatusLive {
		t.Fatalf("recent hour = %v %s want %d", got, coin.RecentHour.Status, sum(32, 91))
	}
	if coin.Hours[1].ChangePct != nil {
		t.Fatal("a running hour was compared with a whole hour")
	}
	if coin.Hours[2].Total.Amount != nil {
		t.Fatal("a future hour has an amount")
	}

	// A minute later the open minute is final and the next one is live.
	now = now.Add(time.Minute)
	drain(t, tr, source, now)
	coin = tr.Snapshot(now).Coins[0]
	if got := amount(coin.Hours[1].Total); got != float64(sum(61, 92)) {
		t.Fatalf("hour 2 after a minute = %v want %d", got, sum(61, 92))
	}
}

func sum(from, to int) int {
	total := 0
	for i := from; i <= to; i++ {
		total += i
	}
	return total
}

func TestDetectedListingWaitsForItsFirstTrade(t *testing.T) {
	detected := time.Date(2026, 10, 12, 5, 30, 10, 0, time.UTC)
	now := detected
	bithumb := mk(market.Bithumb, "ZEST")
	opens := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)
	source := &fakeSource{now: &now, candles: map[market.Key][]candles.Candle{
		bithumb.Key(): series(opens, 120, func(int) float64 { return 5 }),
	}}
	tr := New(fakeLookup{"ZEST": {bithumb}}, 7*24*time.Hour)
	tr.SetClock(func() time.Time { return now })
	tr.AddListing(Listing{Market: bithumb, DetectedAt: detected, Source: SourceLive})
	drain(t, tr, source, now)
	if coin := tr.Snapshot(now).Coins[0]; coin.State != "WAITING" || coin.AnchorAt != nil {
		t.Fatalf("before trading: state=%s", coin.State)
	}

	now = opens.Add(3*time.Minute + 30*time.Second)
	drain(t, tr, source, now)
	coin := tr.Snapshot(now).Coins[0]
	if coin.State != "TRACKING" || *coin.AnchorAt != opens.UnixMilli() {
		t.Fatalf("after trading: state=%s anchor=%v", coin.State, coin.AnchorAt)
	}
	if got := amount(coin.Hours[0].Total); got != 4*5 {
		t.Fatalf("first minutes = %v", got)
	}
}

func TestFailedReadsLeaveUnknownMinutesThenFillThem(t *testing.T) {
	first := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	now := first.Add(10*time.Minute + 30*time.Second)
	upbit := mk(market.Upbit, "GAP")
	source := &fakeSource{now: &now, candles: map[market.Key][]candles.Candle{
		upbit.Key(): series(first, 24*60, func(int) float64 { return 1 }),
	}}
	tr := New(fakeLookup{"GAP": {upbit}}, 7*24*time.Hour)
	tr.SetClock(func() time.Time { return now })
	tr.AddListing(Listing{Market: upbit, FirstTrade: first, Source: SourceScan})
	drain(t, tr, source, now)

	source.fail = true
	for i := 0; i < 80; i++ { // 80 minutes of failed reads
		now = now.Add(time.Minute)
		drain(t, tr, source, now)
	}
	coin := tr.Snapshot(now).Coins[0]
	if coin.Hours[0].Status != StatusPartial {
		t.Fatalf("hour 1 with unread minutes: %s", coin.Hours[0].Status)
	}
	if got := amount(coin.Hours[0].Total); got >= 60 {
		t.Fatalf("unread minutes were counted: %v", got)
	}
	if coin.Exchanges["upbit"].LastError == "" {
		t.Fatal("read error not reported")
	}

	source.fail = false
	for i := 0; i < 70; i++ { // backoff can reach a minute
		now = now.Add(time.Second)
		drain(t, tr, source, now)
	}
	coin = tr.Snapshot(now).Coins[0]
	if coin.Hours[0].Status != StatusFinal || amount(coin.Hours[0].Total) != 60 {
		t.Fatalf("hour 1 after recovery: %s %v", coin.Hours[0].Status, amount(coin.Hours[0].Total))
	}
}

func TestListingsOfOneCoinJoinWithin24Hours(t *testing.T) {
	first := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	now := first.Add(5 * time.Hour)
	tr := New(fakeLookup{}, 7*24*time.Hour)
	tr.SetClock(func() time.Time { return now })
	tr.AddListing(Listing{Market: mk(market.Upbit, "TWO"), FirstTrade: first.Add(2 * time.Hour), Source: SourceScan})
	tr.AddListing(Listing{Market: mk(market.Bithumb, "TWO"), FirstTrade: first, Source: SourceScan})
	tr.AddListing(Listing{Market: mk(market.Upbit, "TWO"), FirstTrade: first.Add(-30 * time.Hour), Source: SourceScan})
	snap := tr.Snapshot(now)
	if len(snap.Coins) != 2 {
		t.Fatalf("coins=%d", len(snap.Coins))
	}
	joined := snap.Coins[0]
	if *joined.AnchorAt != first.UnixMilli() || !joined.Exchanges["upbit"].Listed || !joined.Exchanges["bithumb"].NewlyListed {
		t.Fatalf("joined coin %+v", joined)
	}
}

func TestMarketShareComparesTheDayBeforeWithTheTimeSince(t *testing.T) {
	first := time.Date(2026, 10, 12, 5, 10, 0, 0, time.UTC) // 14:10 KST
	now := first.Add(3 * time.Hour)
	tr := New(fakeLookup{}, 7*24*time.Hour)
	tr.SetClock(func() time.Time { return now })
	var asked [][2]int64
	tr.SetShareSource(func(from, to int64) ShareSum {
		asked = append(asked, [2]int64{from, to})
		if to < first.Unix()/3600 {
			return ShareSum{Venues: map[string]float64{"bithumb": 30, "upbit": 60, "coinone": 10}, Total: 100, Status: StatusFinal}
		}
		return ShareSum{Venues: map[string]float64{"bithumb": 20, "upbit": 80}, Total: 100, Status: StatusFinal}
	})
	tr.AddListing(Listing{Market: mk(market.Upbit, "RSR"), FirstTrade: first, Source: SourceScan})
	ms := tr.Snapshot(now).Coins[0].MarketShare
	if ms == nil || *ms.Before.Share != 30 || *ms.After.Share != 20 {
		t.Fatalf("market share %+v", ms)
	}
	listingHour := first.Unix() / 3600
	if asked[0] != [2]int64{listingHour - 24, listingHour - 1} || asked[1] != [2]int64{listingHour, now.Unix() / 3600} {
		t.Fatalf("windows asked %v", asked)
	}
	if ms.Before.Status != StatusFinal || ms.After.Status != StatusLive {
		t.Fatalf("statuses %s %s", ms.Before.Status, ms.After.Status)
	}
	// After the 24 hours both windows are 24 whole hours long.
	later := first.Add(30 * time.Hour)
	ms = tr.Snapshot(later).Coins[0].MarketShare
	if hours := (ms.After.To - ms.After.From) / 3600_000; hours != 24 || (ms.Before.To-ms.Before.From)/3600_000 != 24 {
		t.Fatalf("after window %d hours, before %d", hours, (ms.Before.To-ms.Before.From)/3600_000)
	}
}

func TestOldSessionsLeaveTheWindow(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	tr := New(fakeLookup{}, 7*24*time.Hour)
	tr.SetClock(func() time.Time { return now })
	tr.AddListing(Listing{Market: mk(market.Upbit, "OLD"), FirstTrade: now.Add(-8 * 24 * time.Hour), Source: SourceScan})
	tr.Maintain(now)
	if len(tr.Snapshot(now).Coins) != 0 {
		t.Fatal("a listing older than the window is still shown")
	}
}
