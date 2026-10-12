package notices

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"listing-tracker/internal/market"
	"listing-tracker/internal/restclient"
)

const (
	DefaultBithumbInterval = 3 * time.Second
	DefaultUpbitInterval   = 5 * time.Second
	pingInterval           = 20 * time.Second
)

type Fetcher interface {
	Get(context.Context, market.Exchange, restclient.Group, string) ([]byte, error)
}

// UpbitKey is an Upbit Open API key. The announcement WebSocket needs no
// permission scope, only a signed token.
type UpbitKey struct {
	Access string
	Secret string
}

func (k UpbitKey) Valid() bool { return k.Access != "" && k.Secret != "" }

type Status struct {
	Mode          string    `json:"mode"` // LIST, WEBSOCKET
	LastSuccessAt time.Time `json:"last_success_at"`
	LastError     string    `json:"last_error,omitempty"`
	LastErrorAt   time.Time `json:"last_error_at"`
}

type Watcher struct {
	fetcher  Fetcher
	upbitKey UpbitKey
	onNotice func(Notice)
	log      *slog.Logger

	mu     sync.Mutex
	status map[market.Exchange]*Status
}

func NewWatcher(fetcher Fetcher, upbitKey UpbitKey, onNotice func(Notice), logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	w := &Watcher{fetcher: fetcher, upbitKey: upbitKey, onNotice: onNotice, log: logger, status: map[market.Exchange]*Status{}}
	w.status[market.Bithumb] = &Status{Mode: "LIST"}
	w.status[market.Upbit] = &Status{Mode: "LIST"}
	if upbitKey.Valid() {
		w.status[market.Upbit].Mode = "WEBSOCKET"
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
		if w.upbitKey.Valid() {
			// One list read fills recent history; the socket then pushes.
			w.pollOnce(ctx, market.Upbit, UpbitListURL, ParseUpbitList)
			w.runUpbitSocket(ctx)
			return
		}
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

func (w *Watcher) runUpbitSocket(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := w.upbitSocketOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		w.record(market.Upbit, err)
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		w.log.Warn("업비트 공지 웹소켓 끊김, 다시 연결합니다", "대기", backoff, "내용", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (w *Watcher) upbitSocketOnce(ctx context.Context) error {
	token, err := upbitToken(w.upbitKey)
	if err != nil {
		return err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.Dial(dialCtx, UpbitWebSocketURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)
	request, _ := json.Marshal([]any{
		map[string]any{"ticket": "listing-tracker-" + nonce()[:8]},
		map[string]any{"type": "announcement", "categories": []string{"trade"}, "include_body": false},
		map[string]any{"format": "DEFAULT"},
	})
	if err := conn.Write(ctx, websocket.MessageText, request); err != nil {
		return err
	}
	connCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-connCtx.Done():
				return
			case <-ticker.C:
				pingCtx, cancel := context.WithTimeout(connCtx, 10*time.Second)
				err := conn.Ping(pingCtx)
				cancel()
				if err != nil {
					stop()
					return
				}
				w.record(market.Upbit, nil)
			}
		}
	}()
	w.record(market.Upbit, nil)
	for {
		_, payload, err := conn.Read(connCtx)
		if err != nil {
			return err
		}
		n, ok, err := ParseUpbitAnnouncement(payload)
		if err != nil {
			return err
		}
		if ok {
			w.record(market.Upbit, nil)
			w.onNotice(n)
		}
	}
}

// upbitToken signs {access_key, nonce} with HS512, the documented format for
// a request without query parameters.
func upbitToken(key UpbitKey) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "HS512", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]string{"access_key": key.Access, "nonce": nonce()})
	if err != nil {
		return "", err
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode(header) + "." + encode(payload)
	mac := hmac.New(sha512.New, []byte(key.Secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + encode(mac.Sum(nil)), nil
}

func nonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
