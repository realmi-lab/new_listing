package tracker

import (
	"sort"
	"time"

	"listing-tracker/internal/market"
	"listing-tracker/internal/notices"
)

const ContractVersion = "LISTING_TRACKER_V2"

// Hour and cell statuses. A null amount is never an observed 0.
const (
	StatusFinal       = "FINAL"       // every minute read after it closed
	StatusLive        = "LIVE"        // the hour in progress
	StatusProvisional = "PROVISIONAL" // closed, its last minutes not yet re-read
	StatusPartial     = "PARTIAL"     // some minutes could not be read
	StatusPending     = "PENDING"     // not started
	StatusWaiting     = "WAITING"     // no first trade yet
	StatusUnlisted    = "UNLISTED"    // the exchange does not list the coin
)

type Snapshot struct {
	ContractVersion string       `json:"contract_version"`
	GeneratedAt     int64        `json:"generated_at"`
	LookbackSec     int64        `json:"lookback_sec"`
	TrackHours      int          `json:"track_hours"`
	Overview        Overview     `json:"overview"`
	Coins           []Coin       `json:"coins"`
	Notices         []NoticeInfo `json:"notices"`
	Feeds           any          `json:"feeds,omitempty"`
}

type Cell struct {
	Amount *float64 `json:"amount"`
	Share  *float64 `json:"share"`
	Status string   `json:"status"`
}

type Overview struct {
	Tracking int `json:"tracking"`
	Waiting  int `json:"waiting"`
	Complete int `json:"complete"`
	// Amount since the first trade of every tracking coin, with each
	// exchange's share of it.
	Cumulative Cell            `json:"cumulative"`
	Exchanges  map[string]Cell `json:"exchanges"`
}

type Hour struct {
	Index      int             `json:"index"`
	From       int64           `json:"from"`
	To         int64           `json:"to"`
	Status     string          `json:"status"`
	Total      Cell            `json:"total"`
	Exchanges  map[string]Cell `json:"exchanges"` // share within the hour
	Cumulative *float64        `json:"cumulative"`
	// ExchangeCumulative is each exchange's amount from hour 1 to this hour.
	ExchangeCumulative map[string]*float64 `json:"exchange_cumulative"`
	ChangePct          *float64            `json:"change_pct"` // against the previous hour
}

type ExchangeInfo struct {
	Listed       bool     `json:"listed"`
	Market       string   `json:"market,omitempty"`
	NewlyListed  bool     `json:"newly_listed"`
	Relisting    bool     `json:"relisting"`
	FirstTradeAt *int64   `json:"first_trade_at"`
	DetectedAt   *int64   `json:"detected_at"`
	Source       string   `json:"source,omitempty"`
	FinalThrough *int64   `json:"final_through"`
	LastPrice    *float64 `json:"last_price"`
	LastTradeAt  *int64   `json:"last_trade_at"`
	LastSuccess  *int64   `json:"last_success_at"`
	LastError    string   `json:"last_error,omitempty"`
	// Cumulative is this exchange's amount since the coin's first trade,
	// with its share of the coin.
	Cumulative Cell `json:"cumulative"`
	// RecentHour is this exchange's part of the coin's RecentHour.
	RecentHour Cell `json:"recent_hour"`
}

type Coin struct {
	Key         string                  `json:"key"`
	Base        string                  `json:"base"`
	KoreanName  string                  `json:"korean_name"`
	EnglishName string                  `json:"english_name"`
	Source      string                  `json:"source"`
	State       string                  `json:"state"` // WAITING, TRACKING, COMPLETE
	AnchorAt    *int64                  `json:"anchor_at"`
	EndAt       *int64                  `json:"end_at"`
	CurrentHour int                     `json:"current_hour"` // 1..24; 0 waiting; 25 complete
	Exchanges   map[string]ExchangeInfo `json:"exchanges"`
	Hours       []Hour                  `json:"hours"`
	// Cumulative is the amount since the first trade; its share is the
	// coin's part of all tracking coins.
	Cumulative Cell `json:"cumulative"`
	FirstHour  Cell `json:"first_hour"`
	LastHour   Cell `json:"last_hour"` // the hour in progress, or hour 24 when complete
	// MarketShare is Bithumb's overall share around the listing.
	MarketShare *MarketShare `json:"market_share"`
	// RecentHour is the 60 minutes up to now inside the 24 hours (since the
	// first trade when shorter); after the 24 hours it is hour 24.
	RecentHour     Cell         `json:"recent_hour"`
	RecentHourFrom int64        `json:"recent_hour_from,omitempty"`
	Notices        []NoticeInfo `json:"notices"`
}

// ShareWindow is Bithumb's share of the whole KRW market (Bithumb + Upbit,
// every KRW market) over whole clock hours [From, To).
type ShareWindow struct {
	From    int64    `json:"from"`
	To      int64    `json:"to"`
	Bithumb *float64 `json:"bithumb"`
	Upbit   *float64 `json:"upbit"`
	Share   *float64 `json:"share"`
	Status  string   `json:"status"`
}

// MarketShare compares the day before a listing with the time since it.
// Hourly candles start on the hour, so the windows are the 24 whole hours
// before the listing hour, and the listing hour up to now (at most +24h).
type MarketShare struct {
	Before ShareWindow `json:"before"`
	After  ShareWindow `json:"after"`
}

type NoticeInfo struct {
	Exchange    string   `json:"exchange"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Category    string   `json:"category"`
	PublishedAt *int64   `json:"published_at"`
	Source      string   `json:"source"`
	KRW         bool     `json:"krw"`
	Symbols     []string `json:"symbols"`
	// Coin names a tracked coin of one of the symbols and CoinState its state.
	Coin      string `json:"coin,omitempty"`
	CoinState string `json:"coin_state,omitempty"`
}

func ms(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	v := t.UnixMilli()
	return &v
}

func f(v float64) *float64 { return &v }

func share(part, total *float64) *float64 {
	if part == nil || total == nil || *total <= 0 {
		return nil
	}
	return f(*part / *total * 100)
}

// minute reads one minute of a market: final, live, or unknown (nil).
func (tr *track) minute(m int64) (*float64, string) {
	if tr.finalThrough != 0 && m >= tr.finalFrom && m < tr.finalThrough {
		return f(tr.final[m].Amount), StatusFinal
	}
	if c, ok := tr.live[m]; ok {
		return f(c.Amount), StatusLive
	}
	if !tr.liveAt.IsZero() && m >= tr.liveFrom && m <= minuteOf(tr.liveAt) {
		return f(0), StatusLive // read after the minute began; no trade in it
	}
	return nil, StatusPartial
}

// span sums [from, to) of one market, stopping at now.
func (tr *track) span(from, to, nowMin int64) Cell {
	if tr == nil {
		return Cell{Status: StatusUnlisted}
	}
	if from > nowMin {
		return Cell{Status: StatusPending}
	}
	total, final, live, unknown := 0.0, 0, 0, 0
	read := false
	for m := from; m < to && m <= nowMin; m++ {
		amount, status := tr.minute(m)
		switch status {
		case StatusFinal:
			final++
		case StatusLive:
			live++
		default:
			unknown++
		}
		if amount != nil {
			total += *amount
			read = true
		}
	}
	c := Cell{Status: StatusFinal}
	switch {
	case to > nowMin:
		c.Status = StatusLive
	case unknown > 0:
		c.Status = StatusPartial
	case live > 0:
		c.Status = StatusProvisional
	}
	if read {
		c.Amount = f(total)
	}
	return c
}

// combine adds exchange cells of one span.
func combine(cells []Cell) Cell {
	var result Cell
	listed, partial, provisional, live, pending := 0, false, false, false, 0
	for _, c := range cells {
		switch c.Status {
		case StatusUnlisted:
			continue
		case StatusPending:
			pending++
		case StatusPartial:
			partial = true
		case StatusProvisional:
			provisional = true
		case StatusLive:
			live = true
		}
		listed++
		if c.Amount != nil {
			if result.Amount == nil {
				result.Amount = f(0)
			}
			*result.Amount += *c.Amount
		}
	}
	switch {
	case listed == 0:
		result.Status = StatusUnlisted
	case pending == listed:
		result.Status = StatusPending
	case live:
		result.Status = StatusLive
	case partial || result.Amount == nil:
		result.Status = StatusPartial
	case provisional:
		result.Status = StatusProvisional
	default:
		result.Status = StatusFinal
	}
	return result
}

// Snapshot builds the API view. Shares are computed here so every viewer
// shows the same numbers.
func (t *Tracker) Snapshot(now time.Time) Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	nowMin, closed := minuteOf(now), closedBefore(now)
	snapshot := Snapshot{
		ContractVersion: ContractVersion, GeneratedAt: now.UnixMilli(),
		LookbackSec: int64(t.lookback / time.Second), TrackHours: TrackHours,
		Overview: Overview{Exchanges: map[string]Cell{}},
	}
	overviewCells := map[string][]Cell{}
	coinIndex := map[string]int{}
	for _, s := range sortedSessions(t.sessions) {
		coin := t.coinLocked(s, nowMin, closed)
		switch coin.State {
		case "WAITING":
			snapshot.Overview.Waiting++
		case "COMPLETE":
			snapshot.Overview.Complete++
		default:
			snapshot.Overview.Tracking++
			for exchange, info := range coin.Exchanges {
				if info.Listed {
					overviewCells[exchange] = append(overviewCells[exchange], info.Cumulative)
				}
			}
		}
		snapshot.Coins = append(snapshot.Coins, coin)
		if _, seen := coinIndex[s.base]; !seen {
			coinIndex[s.base] = len(snapshot.Coins) - 1
		}
	}
	all := make([]Cell, 0, len(market.Exchanges))
	for _, exchange := range market.Exchanges {
		c := combine(overviewCells[string(exchange)])
		snapshot.Overview.Exchanges[string(exchange)] = c
		all = append(all, c)
	}
	snapshot.Overview.Cumulative = combine(all)
	for exchange, c := range snapshot.Overview.Exchanges {
		c.Share = share(c.Amount, snapshot.Overview.Cumulative.Amount)
		snapshot.Overview.Exchanges[exchange] = c
	}
	for i := range snapshot.Coins {
		if snapshot.Coins[i].State == "TRACKING" {
			snapshot.Coins[i].Cumulative.Share = share(snapshot.Coins[i].Cumulative.Amount, snapshot.Overview.Cumulative.Amount)
		}
	}

	cutoff := now.Add(-t.lookback)
	list := make([]notices.Notice, 0, len(t.notices))
	for _, n := range t.notices {
		if n.Listing && (n.PublishedAt.IsZero() || n.PublishedAt.After(cutoff)) {
			list = append(list, n)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].PublishedAt.After(list[j].PublishedAt) })
	for _, n := range list {
		info := noticeInfo(n)
		for _, symbol := range n.Symbols {
			if i, ok := coinIndex[symbol]; ok {
				coin := &snapshot.Coins[i]
				info.Coin, info.CoinState = coin.Base, coin.State
				coin.Notices = append(coin.Notices, noticeInfo(n))
				break
			}
		}
		snapshot.Notices = append(snapshot.Notices, info)
	}
	return snapshot
}

func (t *Tracker) shareWindow(from, to int64, live bool) ShareWindow {
	w := ShareWindow{From: from * 3600 * 1000, To: (to + 1) * 3600 * 1000}
	bithumb, upbit, status := t.share(from, to)
	w.Status = status
	if status == StatusWaiting {
		return w
	}
	w.Bithumb, w.Upbit = f(bithumb), f(upbit)
	w.Share = share(w.Bithumb, f(bithumb+upbit))
	if live && status == StatusFinal {
		w.Status = StatusLive
	}
	return w
}

func noticeInfo(n notices.Notice) NoticeInfo {
	return NoticeInfo{
		Exchange: string(n.Exchange), Title: n.Title, URL: n.URL, Category: n.Category,
		PublishedAt: ms(n.PublishedAt), Source: n.Source, KRW: n.KRW, Symbols: n.Symbols,
	}
}

func (t *Tracker) coinLocked(s *session, nowMin, closed int64) Coin {
	coin := Coin{
		Key: s.base, Base: s.base, KoreanName: s.koreanName, EnglishName: s.englishName,
		Source: s.source, State: "WAITING", Exchanges: map[string]ExchangeInfo{},
		Cumulative: Cell{Status: StatusWaiting}, FirstHour: Cell{Status: StatusWaiting}, LastHour: Cell{Status: StatusWaiting},
		RecentHour: Cell{Status: StatusWaiting},
	}
	if s.anchor != 0 {
		coin.Key = s.base + "-" + timeOf(s.anchor).Format("20060102T1504")
		coin.AnchorAt, coin.EndAt = ms(timeOf(s.anchor)), ms(timeOf(s.end()))
		coin.State = "TRACKING"
		coin.CurrentHour = int((nowMin-s.anchor)/60) + 1
		if s.complete(closed) {
			coin.State, coin.CurrentHour = "COMPLETE", TrackHours+1
		}
	}
	for _, exchange := range market.Exchanges {
		tr := s.tracks[exchange]
		info := ExchangeInfo{Cumulative: Cell{Status: StatusUnlisted}, RecentHour: Cell{Status: StatusUnlisted}}
		if tr != nil {
			info = ExchangeInfo{
				Listed: true, Market: tr.market.Code, NewlyListed: tr.newlyListed, Relisting: tr.relisting,
				DetectedAt: ms(tr.detectedAt), Source: tr.source, LastSuccess: ms(tr.lastSuccess), LastError: tr.lastError,
				LastTradeAt: ms(tr.lastTradeAt), Cumulative: Cell{Status: StatusWaiting}, RecentHour: Cell{Status: StatusWaiting},
			}
			if tr.firstConfirmed {
				info.FirstTradeAt = ms(timeOf(tr.firstTrade))
			}
			if tr.finalThrough != 0 {
				info.FinalThrough = ms(timeOf(tr.finalThrough))
			}
			if !tr.lastTradeAt.IsZero() {
				info.LastPrice = f(tr.lastClose)
			}
		}
		coin.Exchanges[string(exchange)] = info
	}
	if s.anchor == 0 {
		return coin
	}
	if t.share != nil {
		// Both windows are 24 whole hours: the day before the listing hour,
		// and the listing hour plus the 23 after it.
		anchorHour, nowHour := s.anchor/60, nowMin/60
		endHour := anchorHour + TrackHours - 1
		coin.MarketShare = &MarketShare{
			Before: t.shareWindow(anchorHour-24, anchorHour-1, false),
			After:  t.shareWindow(anchorHour, min(nowHour, endHour), nowHour <= endHour),
		}
	}

	var cumulative *float64
	var previous *float64
	exchangeCumulative := map[string]*float64{}
	cumulativeCells := map[string][]Cell{}
	for index := 1; index <= TrackHours; index++ {
		from := s.anchor + int64(index-1)*60
		to := from + 60
		hour := Hour{Index: index, From: timeOf(from).UnixMilli(), To: timeOf(to).UnixMilli(),
			Exchanges: map[string]Cell{}, ExchangeCumulative: map[string]*float64{}}
		cells := make([]Cell, 0, len(market.Exchanges))
		for _, exchange := range market.Exchanges {
			c := s.tracks[exchange].span(from, to, nowMin)
			hour.Exchanges[string(exchange)] = c
			cells = append(cells, c)
			if c.Status != StatusPending && c.Status != StatusUnlisted {
				cumulativeCells[string(exchange)] = append(cumulativeCells[string(exchange)], c)
				if c.Amount != nil {
					if exchangeCumulative[string(exchange)] == nil {
						exchangeCumulative[string(exchange)] = f(0)
					}
					*exchangeCumulative[string(exchange)] += *c.Amount
				}
				if total := exchangeCumulative[string(exchange)]; total != nil {
					hour.ExchangeCumulative[string(exchange)] = f(*total)
				}
			}
		}
		hour.Total = combine(cells)
		hour.Status = hour.Total.Status
		for exchange, c := range hour.Exchanges {
			c.Share = share(c.Amount, hour.Total.Amount)
			hour.Exchanges[exchange] = c
		}
		if hour.Status != StatusPending {
			if hour.Total.Amount != nil {
				if cumulative == nil {
					cumulative = f(0)
				}
				*cumulative += *hour.Total.Amount
			}
			if cumulative != nil {
				hour.Cumulative = f(*cumulative)
			}
			// A running hour is not compared with a whole one.
			if hour.Status != StatusLive && previous != nil && *previous > 0 && hour.Total.Amount != nil {
				hour.ChangePct = f((*hour.Total.Amount / *previous - 1) * 100)
			}
			previous = hour.Total.Amount
		}
		coin.Hours = append(coin.Hours, hour)
	}
	coin.FirstHour = coin.Hours[0].Total
	from, to := max(nowMin-59, s.anchor), min(nowMin+1, s.end())
	if nowMin >= s.end() {
		from, to = s.end()-60, s.end()
	}
	recent := make([]Cell, 0, len(market.Exchanges))
	for _, exchange := range market.Exchanges {
		c := s.tracks[exchange].span(from, to, nowMin)
		recent = append(recent, c)
		if info := coin.Exchanges[string(exchange)]; info.Listed {
			info.RecentHour = c
			coin.Exchanges[string(exchange)] = info
		}
	}
	coin.RecentHour, coin.RecentHourFrom = combine(recent), timeOf(from).UnixMilli()
	for exchange, info := range coin.Exchanges {
		if info.Listed {
			info.RecentHour.Share = share(info.RecentHour.Amount, coin.RecentHour.Amount)
			coin.Exchanges[exchange] = info
		}
	}
	last := min(max(coin.CurrentHour, 1), TrackHours)
	coin.LastHour = coin.Hours[last-1].Total

	exchangeTotals := make([]Cell, 0, len(market.Exchanges))
	for _, exchange := range market.Exchanges {
		info := coin.Exchanges[string(exchange)]
		if !info.Listed {
			continue
		}
		info.Cumulative = combine(cumulativeCells[string(exchange)])
		coin.Exchanges[string(exchange)] = info
		exchangeTotals = append(exchangeTotals, info.Cumulative)
	}
	coin.Cumulative = combine(exchangeTotals)
	for exchange, info := range coin.Exchanges {
		if info.Listed {
			info.Cumulative.Share = share(info.Cumulative.Amount, coin.Cumulative.Amount)
			coin.Exchanges[exchange] = info
		}
	}
	if coin.State == "COMPLETE" && coin.Cumulative.Status == StatusLive {
		coin.Cumulative.Status = StatusFinal
	}
	return coin
}
