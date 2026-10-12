// Package registry watches the official Upbit and Bithumb market lists and
// reports a KRW market that appears after the stored baseline as a listing.
package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"listing-tracker/internal/market"
	"listing-tracker/internal/restclient"
)

const (
	DefaultInterval = 5 * time.Second
	// A market leaves the active set only after this many consecutive polls
	// without it, so one incomplete answer does not create a later "listing".
	RemoveAfterMissingPolls = 3
	// An answer keeping fewer than this fraction of the active markets is
	// treated as an exchange-side fault, not as mass delisting.
	minimumKeptFraction = 0.5
)

type KnownMarket struct {
	market.Market
	FirstSeenAt  time.Time
	UpdatedAt    time.Time
	Active       bool
	Baseline     bool
	MissingPolls int
}

// Update is one accepted poll's changes, persisted in one transaction.
type Update struct {
	Exchange market.Exchange
	At       time.Time
	Markets  []KnownMarket
	Listings []market.Listing
}

func (u Update) empty() bool { return len(u.Markets) == 0 && len(u.Listings) == 0 }

type Store interface {
	LoadKnownMarkets(context.Context) ([]KnownMarket, error)
	ApplyRegistry(context.Context, Update) error
}

type Fetcher interface {
	Get(context.Context, market.Exchange, restclient.Group, string) ([]byte, error)
}

type Status struct {
	LastAttemptAt time.Time `json:"last_attempt_at"`
	LastSuccessAt time.Time `json:"last_success_at"`
	LastError     string    `json:"last_error,omitempty"`
	ActiveMarkets int       `json:"active_markets"`
	BaselineReady bool      `json:"baseline_ready"`
	PendingWrites int       `json:"pending_writes"`
}

type Watcher struct {
	fetcher   Fetcher
	store     Store
	interval  time.Duration
	onListing func(market.Listing)
	now       func() time.Time
	log       *slog.Logger

	mu      sync.Mutex
	known   map[market.Exchange]map[string]KnownMarket
	status  map[market.Exchange]*Status
	polled  map[market.Exchange]bool
	pending []Update
	flushMu sync.Mutex
}

func New(fetcher Fetcher, store Store, interval time.Duration, onListing func(market.Listing), logger *slog.Logger) *Watcher {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	w := &Watcher{
		fetcher: fetcher, store: store, interval: interval, onListing: onListing,
		now: time.Now, log: logger,
		known:  map[market.Exchange]map[string]KnownMarket{},
		status: map[market.Exchange]*Status{},
		polled: map[market.Exchange]bool{},
	}
	for _, exchange := range market.Exchanges {
		w.known[exchange] = map[string]KnownMarket{}
		w.status[exchange] = &Status{}
	}
	return w
}

// Restore loads a stored baseline so the first poll after a restart can
// report markets added while the process was down. Without a store the first
// poll of each run is the baseline.
func (w *Watcher) Restore(ctx context.Context) error {
	if w.store == nil {
		return nil
	}
	rows, err := w.store.LoadKnownMarkets(ctx)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, row := range rows {
		if known, ok := w.known[row.Exchange]; ok {
			known[row.Code] = row
		}
	}
	for exchange, known := range w.known {
		w.status[exchange].BaselineReady = len(known) > 0
		w.status[exchange].ActiveMarkets = countActive(known)
	}
	return nil
}

func (w *Watcher) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, exchange := range market.Exchanges {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(w.interval)
			defer ticker.Stop()
			failing := false
			for {
				err := w.PollOnce(ctx, exchange)
				switch {
				case ctx.Err() != nil:
				case err != nil && !failing:
					w.log.Warn(exchange.Label()+" 마켓 목록 확인 실패, 계속 다시 시도합니다", "내용", err)
					failing = true
				case err == nil && failing:
					w.log.Info(exchange.Label() + " 마켓 목록 확인 복구")
					failing = false
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	wg.Wait()
}

// PollOnce fetches one exchange's list, applies an acceptable answer and
// reports its listings after they are recorded in memory.
func (w *Watcher) PollOnce(ctx context.Context, exchange market.Exchange) error {
	attempt := w.now()
	w.mu.Lock()
	w.status[exchange].LastAttemptAt = attempt
	w.mu.Unlock()

	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	body, err := w.fetcher.Get(requestCtx, exchange, restclient.GroupMarket, market.MarketListURLs[exchange])
	cancel()
	var markets []market.Market
	if err == nil {
		markets, err = market.ParseMarketList(exchange, body)
	}
	if err != nil {
		w.fail(exchange, err)
		return err
	}

	at := w.now()
	w.mu.Lock()
	update, err := Diff(exchange, w.known[exchange], markets, at, !w.polled[exchange])
	if err != nil {
		w.mu.Unlock()
		w.fail(exchange, err)
		return err
	}
	for _, row := range update.Markets {
		w.known[exchange][row.Code] = row
	}
	w.polled[exchange] = true
	status := w.status[exchange]
	status.LastSuccessAt = at
	status.LastError = ""
	status.ActiveMarkets = countActive(w.known[exchange])
	status.BaselineReady = true
	if !update.empty() {
		w.pending = append(w.pending, update)
	}
	w.mu.Unlock()

	// Detection is time critical: tracking starts even when the database is
	// briefly unavailable, and the write is retried on the next poll.
	w.flush(ctx)
	for _, listing := range update.Listings {
		if w.onListing != nil {
			w.onListing(listing)
		}
	}
	return nil
}

func (w *Watcher) fail(exchange market.Exchange, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status[exchange].LastError = err.Error()
}

func (w *Watcher) flush(ctx context.Context) {
	w.flushMu.Lock()
	defer w.flushMu.Unlock()
	for {
		w.mu.Lock()
		if len(w.pending) == 0 {
			w.updatePendingCount()
			w.mu.Unlock()
			return
		}
		next := w.pending[0]
		w.mu.Unlock()
		if w.store == nil {
			w.mu.Lock()
			w.pending = w.pending[1:]
			w.mu.Unlock()
			continue
		}
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := w.store.ApplyRegistry(writeCtx, next)
		cancel()
		w.mu.Lock()
		if err != nil {
			w.status[next.Exchange].LastError = "목록 저장 실패: " + err.Error()
			w.updatePendingCount()
			w.mu.Unlock()
			return
		}
		w.pending = w.pending[1:]
		w.mu.Unlock()
	}
}

func (w *Watcher) updatePendingCount() {
	counts := map[market.Exchange]int{}
	for _, update := range w.pending {
		counts[update.Exchange]++
	}
	for exchange, status := range w.status {
		status.PendingWrites = counts[exchange]
	}
}

// Diff compares one market list answer with the known markets of its
// exchange. With no known markets the answer becomes the baseline.
func Diff(exchange market.Exchange, known map[string]KnownMarket, fetched []market.Market, at time.Time, firstPoll bool) (Update, error) {
	update := Update{Exchange: exchange, At: at}
	if len(fetched) == 0 {
		return update, errors.New("empty market list")
	}
	active := countActive(known)
	if active > 0 && float64(len(fetched)) < float64(active)*minimumKeptFraction {
		return update, fmt.Errorf("market list shrank from %d to %d; answer ignored", active, len(fetched))
	}
	baseline := len(known) == 0
	present := make(map[string]bool, len(fetched))
	for _, m := range fetched {
		present[m.Code] = true
		previous, ok := known[m.Code]
		switch {
		case !ok:
			update.Markets = append(update.Markets, KnownMarket{
				Market: m, FirstSeenAt: at, UpdatedAt: at, Active: true, Baseline: baseline,
			})
			if !baseline {
				update.Listings = append(update.Listings, market.Listing{
					Market: m, DetectedAt: at, OnStartup: firstPoll, Source: market.SourceRegistry,
				})
			}
		case !previous.Active:
			row := previous
			row.Market, row.Active, row.MissingPolls, row.UpdatedAt = m, true, 0, at
			update.Markets = append(update.Markets, row)
			update.Listings = append(update.Listings, market.Listing{
				Market: m, DetectedAt: at, Relisting: true, OnStartup: firstPoll, Source: market.SourceRegistry,
			})
		case previous.MissingPolls != 0 || previous.KoreanName != m.KoreanName || previous.EnglishName != m.EnglishName:
			row := previous
			row.Market, row.MissingPolls, row.UpdatedAt = m, 0, at
			update.Markets = append(update.Markets, row)
		}
	}
	codes := make([]string, 0)
	for code, previous := range known {
		if previous.Active && !present[code] {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	for _, code := range codes {
		row := known[code]
		row.MissingPolls++
		row.UpdatedAt = at
		if row.MissingPolls >= RemoveAfterMissingPolls {
			row.Active = false
		}
		update.Markets = append(update.Markets, row)
	}
	return update, nil
}

func countActive(known map[string]KnownMarket) int {
	count := 0
	for _, row := range known {
		if row.Active {
			count++
		}
	}
	return count
}

// WaitBaseline blocks until every exchange has an accepted market list.
func (w *Watcher) WaitBaseline(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		w.mu.Lock()
		ready := true
		for _, exchange := range market.Exchanges {
			ready = ready && w.polled[exchange]
		}
		w.mu.Unlock()
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Markets returns the active KRW markets of one exchange.
func (w *Watcher) Markets(exchange market.Exchange) []market.Market {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make([]market.Market, 0, len(w.known[exchange]))
	for _, row := range w.known[exchange] {
		if row.Active {
			result = append(result, row.Market)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result
}

// ActiveMarketsForBase returns the active KRW market of each exchange that
// lists the base asset.
func (w *Watcher) ActiveMarketsForBase(base string) []market.Market {
	code := market.Code(base)
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make([]market.Market, 0, len(market.Exchanges))
	for _, exchange := range market.Exchanges {
		if row, ok := w.known[exchange][code]; ok && row.Active {
			result = append(result, row.Market)
		}
	}
	return result
}

func (w *Watcher) Statuses() map[market.Exchange]Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make(map[market.Exchange]Status, len(w.status))
	for exchange, status := range w.status {
		result[exchange] = *status
	}
	return result
}
