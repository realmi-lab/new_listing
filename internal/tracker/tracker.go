// Package tracker builds each listing's first 24 hours from one-minute
// candles. Hours are counted from the listing's first trade minute: hour 1 is
// [first, first+60m). A closed minute is final once read after it closed; the
// minute in progress is re-read every second or two and shown as live.
package tracker

import (
	"sort"
	"strings"
	"sync"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/market"
	"listing-tracker/internal/notices"
)

const (
	TrackHours   = 24
	trackMinutes = TrackHours * 60
	// A minute is read as final this long after it closed.
	closeMargin = 2 * time.Second
	probeEvery  = 3 * time.Second
	maxBackoff  = time.Minute
	maxNotices  = 300
)

const (
	SourceScan   = "SCAN"   // found by the startup scan
	SourceLive   = "LIVE"   // appeared in a market list while running
	SourceManual = "MANUAL" // requested with -watch
)

// Lookup answers which exchanges currently list a base asset in KRW.
type Lookup interface {
	ActiveMarketsForBase(base string) []market.Market
}

// Listing is one market reported as new, with its first trade minute when
// it already traded.
type Listing struct {
	Market     market.Market
	FirstTrade time.Time
	Relisting  bool
	DetectedAt time.Time
	Source     string
}

type track struct {
	market         market.Market
	newlyListed    bool
	relisting      bool
	firstTrade     int64 // minute index of the first candle; 0 if unknown
	firstConfirmed bool
	detectedAt     time.Time
	source         string

	// Final minutes [finalFrom, finalThrough). A minute in range without an
	// entry had no trade.
	final                   map[int64]candles.Candle
	finalFrom, finalThrough int64

	// The latest read's minutes at or after finalThrough, still open.
	live        map[int64]candles.Candle
	liveFrom    int64 // the latest read is complete from this minute
	liveAt      time.Time
	lastClose   float64
	lastTradeAt time.Time

	nextPoll    time.Time
	failures    int
	lastError   string
	lastSuccess time.Time
}

func newTrack(m market.Market, newlyListed bool, source string) *track {
	return &track{market: m, newlyListed: newlyListed, source: source,
		final: map[int64]candles.Candle{}, live: map[int64]candles.Candle{}}
}

type session struct {
	base, koreanName, englishName string
	source                        string
	anchor                        int64 // first trade minute; 0 while waiting
	created                       time.Time
	tracks                        map[market.Exchange]*track
}

func (s *session) end() int64 { return s.anchor + trackMinutes }

func (s *session) complete(closed int64) bool {
	if s.anchor == 0 || closed < s.end() {
		return false
	}
	for _, t := range s.tracks {
		if t.finalThrough < s.end() || t.finalFrom > s.anchor {
			return false
		}
	}
	return true
}

// recomputeAnchor moves the anchor to the earliest confirmed first trade of
// the session's new markets.
func (s *session) recomputeAnchor() {
	var anchor int64
	for _, t := range s.tracks {
		if t.newlyListed && t.firstConfirmed && (anchor == 0 || t.firstTrade < anchor) {
			anchor = t.firstTrade
		}
	}
	if s.source == SourceManual && s.anchor != 0 {
		return
	}
	s.anchor = anchor
}

type Tracker struct {
	lookup   Lookup
	lookback time.Duration
	now      func() time.Time

	mu       sync.Mutex
	sessions []*session
	manual   map[string]bool
	notices  map[string]notices.Notice

	liveMin  map[market.Exchange]time.Duration
	liveRate map[market.Exchange]float64

	// share sums both exchanges' whole KRW market over clock hours
	// [from, to]; see internal/marketshare.
	share func(from, to int64) (bithumb, upbit float64, status string)
}

func New(lookup Lookup, lookback time.Duration) *Tracker {
	return &Tracker{
		lookup: lookup, lookback: lookback, now: time.Now,
		manual: map[string]bool{}, notices: map[string]notices.Notice{},
		// Live refresh: each tracked market at most once a second, and the
		// whole exchange within half of this process's candle budget.
		liveMin:  map[market.Exchange]time.Duration{market.Upbit: time.Second, market.Bithumb: time.Second},
		liveRate: map[market.Exchange]float64{market.Upbit: 4, market.Bithumb: 10},
	}
}

// SetClock replaces the clock, normally with one corrected to the exchanges.
func (t *Tracker) SetClock(now func() time.Time) { t.now = now }

// SetShareSource gives the whole market totals used for Bithumb's overall
// share before and after each listing.
func (t *Tracker) SetShareSource(window func(from, to int64) (bithumb, upbit float64, status string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.share = window
}

// SetLiveRate sets how many live refreshes per second one exchange may use.
func (t *Tracker) SetLiveRate(exchange market.Exchange, perSecond float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if perSecond > 0 {
		t.liveRate[exchange] = perSecond
	}
}

func minuteOf(t time.Time) int64       { return t.Unix() / 60 }
func timeOf(minute int64) time.Time    { return time.Unix(minute*60, 0).UTC() }
func closedBefore(now time.Time) int64 { return minuteOf(now.Add(-closeMargin)) }

// AddListing starts or joins the session of the listed base. Two listings
// of one base join when their first trades are within the 24 hours.
func (t *Tracker) AddListing(l Listing) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.addListingLocked(l)
}

func (t *Tracker) addListingLocked(l Listing) *session {
	base := strings.ToUpper(l.Market.Base)
	first := int64(0)
	if !l.FirstTrade.IsZero() {
		first = minuteOf(l.FirstTrade)
	}
	var s *session
	for _, candidate := range t.sessions {
		if candidate.base != base || candidate.source == SourceManual {
			continue
		}
		joinable := candidate.anchor == 0 ||
			(first == 0 && minuteOf(t.now()) < candidate.end()) ||
			(first != 0 && abs(first-candidate.anchor) < trackMinutes)
		if joinable {
			s = candidate
			break
		}
	}
	if s == nil {
		s = &session{base: base, source: l.Source, created: t.now(), tracks: map[market.Exchange]*track{}}
		t.sessions = append(t.sessions, s)
	}
	if s.koreanName == "" {
		s.koreanName = l.Market.KoreanName
	}
	if s.englishName == "" {
		s.englishName = l.Market.EnglishName
	}
	tr := s.tracks[l.Market.Exchange]
	if tr == nil {
		tr = newTrack(l.Market, true, l.Source)
		s.tracks[l.Market.Exchange] = tr
	}
	tr.newlyListed, tr.relisting = true, tr.relisting || l.Relisting
	if tr.detectedAt.IsZero() {
		tr.detectedAt = l.DetectedAt
	}
	if first != 0 && (!tr.firstConfirmed || first < tr.firstTrade) {
		tr.firstTrade, tr.firstConfirmed = first, true
	}
	t.attachListedLocked(s)
	s.recomputeAnchor()
	return s
}

// attachListedLocked adds the other exchanges that already list the base,
// so a new Upbit listing is shown with its existing Bithumb market.
func (t *Tracker) attachListedLocked(s *session) {
	if t.lookup == nil {
		return
	}
	for _, m := range t.lookup.ActiveMarketsForBase(s.base) {
		if s.tracks[m.Exchange] == nil {
			s.tracks[m.Exchange] = newTrack(m, false, s.source)
		}
		if s.koreanName == "" {
			s.koreanName = m.KoreanName
		}
		if s.englishName == "" {
			s.englishName = m.EnglishName
		}
	}
}

// AddManual follows a base from the current minute, for coins listed before
// the lookback window or for checking the program.
func (t *Tracker) AddManual(base string) {
	base = strings.ToUpper(strings.TrimSpace(base))
	if base == "" {
		return
	}
	t.mu.Lock()
	t.manual[base] = true
	t.mu.Unlock()
	t.Maintain(t.now())
}

// Maintain resolves manual requests, attaches newly listed exchanges and
// drops sessions older than the lookback window.
func (t *Tracker) Maintain(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for base := range t.manual {
		if t.lookup == nil || len(t.lookup.ActiveMarketsForBase(base)) == 0 {
			continue
		}
		delete(t.manual, base)
		s := &session{base: base, source: SourceManual, created: now, anchor: minuteOf(now), tracks: map[market.Exchange]*track{}}
		t.attachListedLocked(s)
		t.sessions = append(t.sessions, s)
	}
	cutoff := now.Add(-t.lookback)
	kept := t.sessions[:0]
	for _, s := range t.sessions {
		if s.anchor != 0 && timeOf(s.anchor).Before(cutoff) {
			continue
		}
		if s.anchor == 0 && s.created.Before(cutoff) {
			continue
		}
		if !s.complete(closedBefore(now)) {
			t.attachListedLocked(s)
		}
		kept = append(kept, s)
	}
	t.sessions = kept
}

// AddNotice records an announcement and reports whether it is new.
func (t *Tracker) AddNotice(n notices.Notice) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := notices.Key(n)
	if _, ok := t.notices[key]; ok {
		return false
	}
	t.notices[key] = n
	if len(t.notices) > maxNotices {
		oldestKey, oldest := "", time.Time{}
		for k, v := range t.notices {
			if oldestKey == "" || v.PublishedAt.Before(oldest) {
				oldestKey, oldest = k, v.PublishedAt
			}
		}
		delete(t.notices, oldestKey)
	}
	return true
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func sortedSessions(sessions []*session) []*session {
	result := append([]*session(nil), sessions...)
	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if (a.anchor == 0) != (b.anchor == 0) {
			return a.anchor == 0
		}
		if a.anchor != b.anchor {
			return a.anchor > b.anchor
		}
		return a.base < b.base
	})
	return result
}
