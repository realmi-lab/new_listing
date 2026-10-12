package candles

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"listing-tracker/internal/market"
	"listing-tracker/internal/restclient"
)

func TestParseSortsOldestFirstAndReadsAmount(t *testing.T) {
	payload := []byte(`[
	 {"market":"KRW-BTC","candle_date_time_utc":"2026-10-12T06:01:00","trade_price":113204000,"timestamp":1791784864000,"candle_acc_trade_price":232369.812,"candle_acc_trade_volume":0.002053},
	 {"market":"KRW-BTC","candle_date_time_utc":"2026-10-12T06:00:00","trade_price":"113185000","timestamp":"1791784800000","candle_acc_trade_price":"100.5","candle_acc_trade_volume":"0.1"}]`)
	got, err := Parse(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Start.Minute() != 0 || got[1].Amount != 232369.812 || got[0].Amount != 100.5 {
		t.Fatalf("parsed %+v", got)
	}
	if got[0].Start.Location() != time.UTC || got[0].LastTradeAt.IsZero() {
		t.Fatalf("times not UTC or missing: %+v", got[0])
	}
}

func TestParseRejectsErrorObjectAndNegativeAmount(t *testing.T) {
	if _, err := Parse([]byte(`{"error":{"name":"400","message":"bad"}}`)); err == nil {
		t.Fatal("error object accepted")
	}
	if _, err := Parse([]byte(`[{"candle_date_time_utc":"2026-10-12T06:00:00","candle_acc_trade_price":-1,"candle_acc_trade_volume":1}]`)); err == nil {
		t.Fatal("negative amount accepted")
	}
}

// fakeExchange serves one-minute candles for the given minutes, newest
// first, honoring count and the exclusive to.
type fakeExchange struct {
	minutes  []time.Time
	requests []string
}

func (f *fakeExchange) Get(_ context.Context, exchange market.Exchange, _ restclient.Group, raw string) ([]byte, error) {
	f.requests = append(f.requests, raw)
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	count, _ := strconv.Atoi(u.Query().Get("count"))
	to := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC) // no to: the latest candles
	if value := u.Query().Get("to"); value != "" {
		if exchange == market.Upbit {
			to, err = time.Parse("2006-01-02T15:04:05Z", value)
		} else {
			to, err = time.ParseInLocation("2006-01-02T15:04:05", value, KST)
		}
		if err != nil {
			return nil, err
		}
	}
	rows := []string{}
	for i := len(f.minutes) - 1; i >= 0 && len(rows) < count; i-- {
		if f.minutes[i].Before(to) {
			rows = append(rows, fmt.Sprintf(`{"candle_date_time_utc":%q,"trade_price":1,"candle_acc_trade_price":1000,"candle_acc_trade_volume":1}`,
				f.minutes[i].UTC().Format("2006-01-02T15:04:05")))
		}
	}
	return []byte("[" + strings.Join(rows, ",") + "]"), nil
}

func TestMinuteRangePagesBackAndKeepsOnlyRange(t *testing.T) {
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	fake := &fakeExchange{}
	for i := 0; i < 500; i++ {
		if i%7 == 3 {
			continue // minutes without a trade have no candle
		}
		fake.minutes = append(fake.minutes, start.Add(time.Duration(i)*time.Minute))
	}
	client := NewClient(fake)
	for _, exchange := range market.Exchanges {
		fake.requests = nil
		from, to := start.Add(10*time.Minute), start.Add(460*time.Minute)
		got, err := client.MinuteRange(context.Background(), exchange, "KRW-ABC", from, to)
		if err != nil {
			t.Fatal(err)
		}
		var want []time.Time
		for _, m := range fake.minutes {
			if !m.Before(from) && m.Before(to) {
				want = append(want, m)
			}
		}
		if len(got) != len(want) || !got[0].Start.Equal(want[0]) || !got[len(got)-1].Start.Equal(want[len(want)-1]) {
			t.Fatalf("%s: got %d candles [%v..%v], want %d", exchange, len(got), got[0].Start, got[len(got)-1].Start, len(want))
		}
		if len(fake.requests) < 2 {
			t.Fatalf("%s: expected paging, got %d requests", exchange, len(fake.requests))
		}
	}
}

func TestFormatToUsesEachExchangeClock(t *testing.T) {
	at := time.Date(2026, 10, 12, 6, 3, 0, 0, time.UTC)
	if got := formatTo(market.Upbit, at); got != "2026-10-12T06:03:00Z" {
		t.Fatalf("upbit to=%s", got)
	}
	if got := formatTo(market.Bithumb, at); got != "2026-10-12T15:03:00" {
		t.Fatalf("bithumb to=%s", got)
	}
}
