package tracker

import (
	"context"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/market"
)

type CandleSource interface {
	Minutes(ctx context.Context, exchange market.Exchange, code string, unit int, to time.Time, count int) ([]candles.Candle, error)
	MinuteRange(ctx context.Context, exchange market.Exchange, code string, from, to time.Time) ([]candles.Candle, error)
}

type jobKind int

const (
	jobFirst  jobKind = iota + 1 // find the first trade minute of a new market
	jobRange                     // read every minute of [from, to)
	jobLatest                    // read the latest count minutes, the open one included
)

type job struct {
	kind     jobKind
	session  *session
	track    *track
	from, to int64
	count    int
}

// liveIntervalLocked spreads the live refreshes of one exchange inside its
// share of the request budget.
func (t *Tracker) liveIntervalLocked(exchange market.Exchange, now time.Time) time.Duration {
	nowMin := minuteOf(now)
	live := 0
	for _, s := range t.sessions {
		if tr := s.tracks[exchange]; tr != nil && s.anchor != 0 && nowMin < s.end() {
			live++
		}
	}
	interval := time.Duration(float64(live) / t.liveRate[exchange] * float64(time.Second))
	return max(interval, t.liveMin[exchange])
}

// nextJob returns the due job of one exchange that has waited longest, or
// how long until one is due.
func (t *Tracker) nextJob(exchange market.Exchange, now time.Time) (job, time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	closed, nowMin := closedBefore(now), minuteOf(now)
	wait := time.Second
	var best job
	var bestDue time.Time
	found := false
	for _, s := range t.sessions {
		tr := s.tracks[exchange]
		if tr == nil {
			continue
		}
		candidate, ok := planLocked(s, tr, closed, nowMin)
		if !ok {
			continue
		}
		if tr.nextPoll.After(now) {
			wait = min(wait, tr.nextPoll.Sub(now))
			continue
		}
		if !found || tr.nextPoll.Before(bestDue) {
			best, bestDue, found = candidate, tr.nextPoll, true
		}
	}
	return best, wait, found
}

// planLocked decides what one market needs next.
func planLocked(s *session, tr *track, closed, nowMin int64) (job, bool) {
	if tr.newlyListed && !tr.firstConfirmed {
		return job{kind: jobFirst, session: s, track: tr}, true
	}
	if s.anchor == 0 {
		return job{}, false
	}
	end := s.end()
	target := min(closed, end)
	latest := func(from int64) (job, bool) {
		// Two spare candles absorb a small clock difference at the edge.
		span := nowMin - from + 3
		if span <= candles.MaxCount {
			return job{kind: jobLatest, session: s, track: tr, count: int(max(span, 3))}, true
		}
		return job{kind: jobRange, session: s, track: tr, from: from, to: target}, true
	}
	switch {
	case tr.finalThrough == 0:
		if nowMin < end || s.anchor < target {
			return latest(s.anchor)
		}
	case tr.finalFrom > s.anchor:
		return job{kind: jobRange, session: s, track: tr, from: s.anchor, to: tr.finalFrom}, true
	case tr.finalThrough < target:
		return latest(tr.finalThrough)
	case nowMin < end:
		return job{kind: jobLatest, session: s, track: tr, count: 3}, true
	}
	return job{}, false
}

// RunFetcher works through one exchange's candle reads until ctx ends.
func (t *Tracker) RunFetcher(ctx context.Context, exchange market.Exchange, source CandleSource) {
	for ctx.Err() == nil {
		j, wait, ok := t.nextJob(exchange, t.now())
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-time.After(min(wait, 250*time.Millisecond)):
			}
			continue
		}
		timeout := 10 * time.Second
		if j.kind != jobLatest {
			timeout = 60 * time.Second
		}
		jobCtx, cancel := context.WithTimeout(ctx, timeout)
		result, err := t.execute(jobCtx, source, exchange, j)
		cancel()
		if ctx.Err() != nil {
			return
		}
		t.apply(j, result, err, t.now())
	}
}

func (t *Tracker) execute(ctx context.Context, source CandleSource, exchange market.Exchange, j job) ([]candles.Candle, error) {
	code := j.track.market.Code
	switch j.kind {
	case jobFirst:
		latest, err := source.Minutes(ctx, exchange, code, 1, time.Time{}, candles.MaxCount)
		if err != nil || len(latest) < candles.MaxCount {
			return latest, err
		}
		// Trading began more than 200 minutes ago: page back to its start.
		notBefore := t.now().Add(-t.lookback - 24*time.Hour)
		older, err := source.MinuteRange(ctx, exchange, code, notBefore, latest[0].Start)
		if err != nil {
			return nil, err
		}
		return append(older, latest...), nil
	case jobRange:
		return source.MinuteRange(ctx, exchange, code, timeOf(j.from), timeOf(j.to))
	default:
		return source.Minutes(ctx, exchange, code, 1, time.Time{}, j.count)
	}
}

func (t *Tracker) apply(j job, result []candles.Candle, err error, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr, s := j.track, j.session
	if err != nil {
		tr.failures++
		tr.lastError = err.Error()
		tr.nextPoll = now.Add(min(maxBackoff, time.Duration(1<<min(tr.failures, 6))*time.Second))
		return
	}
	tr.failures, tr.lastError, tr.lastSuccess = 0, "", now
	tr.nextPoll = now
	switch j.kind {
	case jobFirst:
		if len(result) == 0 {
			tr.nextPoll = now.Add(probeEvery)
			return
		}
		tr.firstTrade, tr.firstConfirmed = minuteOf(result[0].Start), true
		s.recomputeAnchor()
	case jobRange:
		tr.addFinal(result, j.from, j.to)
	case jobLatest:
		from := tr.finalThrough
		if from == 0 {
			from = s.anchor
		}
		closed := min(closedBefore(now), s.end())
		covered := len(result) < j.count || (len(result) > 0 && minuteOf(result[0].Start) <= from)
		if covered && from < closed {
			tr.addFinal(result, from, closed)
		}
		tr.live = map[int64]candles.Candle{}
		for _, c := range result {
			if minute := minuteOf(c.Start); minute >= tr.finalThrough && minute >= s.anchor {
				tr.live[minute] = c
			}
		}
		tr.liveFrom = from
		if !covered && len(result) > 0 {
			tr.liveFrom = minuteOf(result[0].Start)
		}
		tr.liveAt = now
		if minuteOf(now) < s.end() {
			tr.nextPoll = now.Add(t.liveIntervalLocked(tr.market.Exchange, now))
		}
	}
	if n := len(result); n > 0 {
		newest := result[n-1]
		if !newest.LastTradeAt.Before(tr.lastTradeAt) {
			tr.lastClose, tr.lastTradeAt = newest.Close, newest.LastTradeAt
		}
	}
}

// addFinal stores the candles of [from, to) as final and extends the final
// range, which stays contiguous because jobs only read next to it.
func (tr *track) addFinal(result []candles.Candle, from, to int64) {
	if to <= from {
		return
	}
	for _, c := range result {
		if minute := minuteOf(c.Start); minute >= from && minute < to {
			tr.final[minute] = c
		}
	}
	switch {
	case tr.finalThrough == 0:
		tr.finalFrom, tr.finalThrough = from, to
	default:
		tr.finalFrom = min(tr.finalFrom, from)
		tr.finalThrough = max(tr.finalThrough, to)
	}
	for minute := range tr.live {
		if minute < tr.finalThrough {
			delete(tr.live, minute)
		}
	}
}
