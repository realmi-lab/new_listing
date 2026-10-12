package notices

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"listing-tracker/internal/market"
	"listing-tracker/internal/restclient"
)

const (
	DefaultBithumbInterval = 3 * time.Second
	DefaultUpbitInterval   = 5 * time.Second
)

type Fetcher interface {
	Get(context.Context, market.Exchange, restclient.Group, string) ([]byte, error)
}

type Status struct {
	LastSuccessAt time.Time `json:"last_success_at"`
	LastError     string    `json:"last_error,omitempty"`
	LastErrorAt   time.Time `json:"last_error_at"`
}

// Watcher reads both exchanges' notice lists on a fixed interval.
type Watcher struct {
	fetcher  Fetcher
	onNotice func(Notice)
	log      *slog.Logger

	mu     sync.Mutex
	status map[market.Exchange]*Status
}

func NewWatcher(fetcher Fetcher, onNotice func(Notice), logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	w := &Watcher{fetcher: fetcher, onNotice: onNotice, log: logger, status: map[market.Exchange]*Status{}}
	for _, exchange := range market.Exchanges {
		w.status[exchange] = &Status{}
	}
	return w
}

func (w *Watcher) Statuses() map[market.Exchange]Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := map[market.Exchange]Status{}
	for exchange, s := range w.status {
		result[exchange] = *s
	}
	return result
}

func (w *Watcher) record(exchange market.Exchange, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.status[exchange]
	if err != nil {
		s.LastError, s.LastErrorAt = err.Error(), time.Now()
		return
	}
	s.LastSuccessAt, s.LastError = time.Now(), ""
}

func (w *Watcher) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		w.poll(ctx, market.Bithumb, BithumbURL, DefaultBithumbInterval, ParseBithumb)
	}()
	go func() {
		defer wg.Done()
		w.poll(ctx, market.Upbit, UpbitListURL, DefaultUpbitInterval, ParseUpbitList)
	}()
	wg.Wait()
}

func (w *Watcher) poll(ctx context.Context, exchange market.Exchange, url string, interval time.Duration, parse func([]byte) ([]Notice, error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		w.pollOnce(ctx, exchange, url, parse)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Watcher) pollOnce(ctx context.Context, exchange market.Exchange, url string, parse func([]byte) ([]Notice, error)) {
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	body, err := w.fetcher.Get(requestCtx, exchange, restclient.GroupNotice, url)
	var rows []Notice
	if err == nil {
		rows, err = parse(body)
	}
	if ctx.Err() != nil {
		return
	}
	w.record(exchange, err)
	if err != nil {
		w.log.Debug(exchange.Label()+" 공지 확인 실패", "내용", err)
		return
	}
	for _, n := range rows {
		w.onNotice(n)
	}
}
