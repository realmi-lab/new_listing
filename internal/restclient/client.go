// Package restclient sends the public REST requests of one process within
// each exchange's per-IP request groups and backs off when an exchange says
// the budget is exhausted.
//
// Upbit counts requests per IP and group (market, ticker, ... 10/s each) and
// Bithumb per IP and category (150/s each). Another process on the same host
// shares those budgets, so this client spaces its own requests well below them.
package restclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"listing-tracker/internal/market"
)

const maxBody = 8 << 20

// Group is the exchange's rate-limit group of a request.
type Group string

const (
	GroupMarket Group = "market"
	GroupCandle Group = "candle"
	GroupNotice Group = "notice"
)

// SetUpbitRate lowers this process's Upbit rate per group, for a host that
// also runs other Upbit clients (they share the per-IP budget).
func (c *Client) SetUpbitRate(perSecond float64) {
	if perSecond > 0 {
		c.upbitGap.Store(int64(float64(time.Second) / min(perSecond, 8)))
	}
}

func (c *Client) gap(exchange market.Exchange, group Group) time.Duration {
	if exchange == market.Upbit && group != GroupNotice {
		if custom := time.Duration(c.upbitGap.Load()); custom > 0 {
			return custom
		}
	}
	return minimumGap(exchange, group)
}

// minimumGap is this process's own spacing per exchange and group.
func minimumGap(exchange market.Exchange, group Group) time.Duration {
	switch {
	case exchange == market.Bithumb && group == GroupNotice:
		return 1100 * time.Millisecond // documented limit: 1 request per second per IP
	case exchange == market.Upbit && group == GroupNotice:
		return 3 * time.Second // web announcement list: no published limit, stay gentle
	case exchange == market.Upbit:
		return 125 * time.Millisecond // at most 8/s of the 10/s group budget
	default:
		return 20 * time.Millisecond // at most 50/s of Bithumb's 150/s category budget
	}
}

// ErrRateLimited reports a 429/418 answer; the client already waits before
// the next request of the same group.
var ErrRateLimited = errors.New("rate limited by exchange")

// StatusError is a non-2xx answer other than a rate limit.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

type slot struct {
	next    time.Time
	backoff time.Duration
}

type Client struct {
	http *http.Client
	mu   sync.Mutex
	// One gate per exchange/group; requests of a group queue on it.
	gates map[string]*sync.Mutex
	slots map[string]*slot
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
	// offset is exchange time minus local time, read from response Date
	// headers: a home PC clock can be minutes off, and minute boundaries
	// must follow the exchange.
	offset   atomic.Int64
	upbitGap atomic.Int64
}

// Now returns the local time corrected to the exchanges' clock.
func (c *Client) Now() time.Time { return time.Now().Add(time.Duration(c.offset.Load())) }

func (c *Client) observeDate(header string, sent, received time.Time) {
	date, err := http.ParseTime(header)
	if err != nil {
		return
	}
	// Date has one-second resolution and is stamped mid-request.
	local := sent.Add(received.Sub(sent) / 2)
	measured := date.Add(500 * time.Millisecond).Sub(local)
	previous := time.Duration(c.offset.Load())
	if previous == 0 || (measured-previous).Abs() > 5*time.Second {
		c.offset.Store(int64(measured))
		return
	}
	c.offset.Store(int64(previous + (measured-previous)/8))
}

// allowedHost reports whether a redirect may go to host: only the two
// exchanges' own domains (Bithumb's notice API answers with a redirect to
// feed-api.bithumb.com).
func allowedHost(host string) bool {
	host = strings.ToLower(host)
	for _, domain := range []string{"upbit.com", "bithumb.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// NewHTTPClient follows at most three redirects, each to an https address
// of Upbit or Bithumb.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if request.URL.Scheme != "https" || !allowedHost(request.URL.Hostname()) {
				return fmt.Errorf("redirect to %s refused", request.URL.Host)
			}
			return nil
		},
	}
}

func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = NewHTTPClient(5 * time.Second)
	}
	return &Client{
		http: httpClient, gates: map[string]*sync.Mutex{}, slots: map[string]*slot{},
		now: time.Now, sleep: sleepContext,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) state(key string) (*sync.Mutex, *slot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gate, ok := c.gates[key]
	if !ok {
		gate = &sync.Mutex{}
		c.gates[key] = gate
		c.slots[key] = &slot{}
	}
	return gate, c.slots[key]
}

// Get waits for the group's next slot, sends one GET and returns the body of
// a 2xx answer.
func (c *Client) Get(ctx context.Context, exchange market.Exchange, group Group, url string) ([]byte, error) {
	key := string(exchange) + "/" + string(group)
	gate, s := c.state(key)
	gate.Lock()
	defer gate.Unlock()
	if err := c.sleep(ctx, s.next.Sub(c.now())); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "listing-tracker/1")
	started := c.now()
	s.next = started.Add(c.gap(exchange, group))
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	c.observeDate(response.Header.Get("Date"), started, c.now())
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, errors.New("response body too large")
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusTeapot {
		// 418 is Upbit's temporary block after repeated 429s: wait longer.
		s.backoff = min(max(time.Second, s.backoff*2), time.Minute)
		if response.StatusCode == http.StatusTeapot {
			s.backoff = time.Minute
		}
		s.next = c.now().Add(s.backoff)
		return nil, ErrRateLimited
	}
	s.backoff = 0
	if exchange == market.Upbit {
		if remaining, ok := remainingPerSecond(response.Header.Get("Remaining-Req")); ok && remaining <= 1 {
			s.next = maxTime(s.next, started.Truncate(time.Second).Add(time.Second))
		}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, &StatusError{Status: response.StatusCode, Body: truncate(string(body), 200)}
	}
	return body, nil
}

// remainingPerSecond reads "group=ticker; min=1800; sec=9".
func remainingPerSecond(header string) (int, bool) {
	for _, part := range strings.Split(header, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(name, "sec") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			return n, err == nil
		}
	}
	return 0, false
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func truncate(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}
