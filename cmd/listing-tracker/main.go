// listing-tracker follows new KRW listings on Upbit and Bithumb. At start it
// finds the listings of the last seven days and rebuilds each one's first 24
// hours from one-minute candles; while running it detects new listings from
// the market lists and announcements and keeps adding minutes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/discovery"
	"listing-tracker/internal/httpapi"
	"listing-tracker/internal/market"
	"listing-tracker/internal/notices"
	"listing-tracker/internal/registry"
	"listing-tracker/internal/restclient"
	"listing-tracker/internal/tracker"
	"listing-tracker/internal/webui"
)

var version = "dev"

func main() {
	useUTF8Console()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		if runtime.GOOS == "windows" {
			fmt.Fprintln(os.Stderr, "Enter 키를 누르면 창이 닫힙니다.")
			_, _ = fmt.Scanln()
		}
		os.Exit(1)
	}
}

func run() error {
	port := flag.Int("port", 8770, "화면 포트 (이 PC 안 127.0.0.1에서만 열림)")
	lookback := flag.Duration("lookback", discovery.DefaultLookback, "켤 때 찾을 상장 기간")
	registryInterval := flag.Duration("registry-interval", registry.DefaultInterval, "마켓 목록 확인 간격")
	watch := flag.String("watch", "", "지금부터 직접 추적할 코인, 쉼표로 구분 (예: BTC,ETH)")
	upbitRate := flag.Float64("upbit-rate", 0, "업비트 그룹별 초당 요청 상한 (같은 IP에서 다른 업비트 프로그램을 쓸 때 낮춤, 최대 8)")
	noBrowser := flag.Bool("no-browser", false, "켤 때 브라우저를 열지 않음")
	verbose := flag.Bool("verbose", false, "자세한 기록 출력")
	flag.Usage = usage
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(newConsoleHandler(os.Stdout, level))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := restclient.New(restclient.NewHTTPClient(8 * time.Second))
	if *upbitRate > 0 {
		client.SetUpbitRate(*upbitRate)
	}
	candleClient := candles.NewClient(client)
	candleClient.SetClock(client.Now)

	var watcher *registry.Watcher
	lookup := lookupFunc(func(base string) []market.Market {
		if watcher == nil {
			return nil
		}
		return watcher.ActiveMarketsForBase(base)
	})
	track := tracker.New(lookup, *lookback)
	track.SetClock(client.Now)
	if *upbitRate > 0 {
		track.SetLiveRate(market.Upbit, max(*upbitRate/2, 0.5))
	}
	watcher = registry.New(client, *registryInterval, func(l market.Listing) {
		line("%s  신규 상장   %s %s — 첫 체결을 기다립니다", label(l.Exchange), l.Code, l.KoreanName)
		track.AddListing(tracker.Listing{Market: l.Market, DetectedAt: l.DetectedAt, Relisting: l.Relisting, Source: tracker.SourceLive})
	}, logger)
	scanner := discovery.New(candleClient, *lookback)
	scanner.SetClock(client.Now)

	started := time.Now()
	var pollMu sync.Mutex
	lastPoll := map[market.Exchange]time.Time{}
	noticeWatcher := notices.NewWatcher(client, func(n notices.Notice) {
		if !track.AddNotice(n) || !n.Listing {
			return
		}
		if time.Since(started) > time.Minute {
			line("%s  상장 공지   %s", label(n.Exchange), n.Title)
		}
		// A new listing notice: read the market list now instead of waiting.
		pollMu.Lock()
		due := time.Since(lastPoll[n.Exchange]) > 2*time.Second
		if due {
			lastPoll[n.Exchange] = time.Now()
		}
		pollMu.Unlock()
		if due {
			go func() { _ = watcher.PollOnce(ctx, n.Exchange) }()
		}
	}, logger)

	// The screen is only the one built into this program.
	var web fs.FS
	if embedded, ok := webui.FS(); ok {
		web = embedded
	}
	feeds := func() any {
		return map[string]any{
			"registry":        watcher.Statuses(),
			"discovery":       scanner.Progress(),
			"notices":         noticeWatcher.Statuses(),
			"clock_offset_ms": client.Now().Sub(time.Now()).Milliseconds(),
			"version":         version,
			"started_at":      started.UnixMilli(),
		}
	}
	// The screen is served on this PC's loopback address only; no option
	// opens it to other PCs.
	if *port < 1024 || *port > 65535 {
		return fmt.Errorf("포트는 1024~65535 사이로 지정하세요")
	}
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(*port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("화면 주소 %s 를 열 수 없습니다 (이미 실행 중이면 -port 로 다른 포트를 지정하세요): %w", addr, err)
	}
	api := httpapi.New(track, feeds, client.Now, web)
	api.LoopbackOnly(fmt.Sprint(*port))
	server := &http.Server{
		Addr: addr, Handler: api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	url := "http://" + listener.Addr().String() + "/"

	banner(version, url, humanDays(*lookback))

	var wg sync.WaitGroup
	start := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	start(func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("화면 서버 오류", "내용", err)
			stop()
		}
	})
	start(func() { watcher.Run(ctx) })
	start(func() { noticeWatcher.Run(ctx) })
	for _, exchange := range market.Exchanges {
		start(func() { track.RunFetcher(ctx, exchange, candleClient) })
		start(func() { scan(ctx, watcher, scanner, track, exchange) })
	}
	start(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			track.Maintain(client.Now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	for _, base := range strings.Split(*watch, ",") {
		if base = strings.TrimSpace(base); base != "" {
			track.AddManual(base)
		}
	}
	if !*noBrowser {
		openBrowser(url)
	}

	<-ctx.Done()
	line("종료하는 중…")
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	wg.Wait()
	return nil
}

// scan waits for the exchange's first market list, then looks for listings
// inside the lookback window.
func scan(ctx context.Context, watcher *registry.Watcher, scanner *discovery.Scanner, track *tracker.Tracker, exchange market.Exchange) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for watcher.Statuses()[exchange].LastSuccessAt.IsZero() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
	markets := watcher.Markets(exchange)
	line("%s  상장 찾기   원화 마켓 %d개 확인 중", label(exchange), len(markets))
	scanner.Scan(ctx, exchange, markets, func(found discovery.Found) {
		if found.FirstTrade.IsZero() {
			line("%s  거래 대기   %s — 아직 첫 체결이 없습니다", label(exchange), found.Market.Code)
		} else {
			line("%s  최근 상장   %-14s 첫 체결 %s", label(exchange), found.Market.Code, found.FirstTrade.In(candles.KST).Format("01-02 15:04"))
		}
		track.AddListing(tracker.Listing{Market: found.Market, FirstTrade: found.FirstTrade, Relisting: found.Relisting, Source: tracker.SourceScan})
	})
	if ctx.Err() == nil {
		p := scanner.Progress()[exchange]
		line("%s  확인 완료   최근 상장 %d개 · 조회 실패 %d개", label(exchange), p.Found, p.Errors)
	}
}

type lookupFunc func(string) []market.Market

func (f lookupFunc) ActiveMarketsForBase(base string) []market.Market { return f(base) }

func humanDays(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d일", int(d/(24*time.Hour)))
	}
	return d.String()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
