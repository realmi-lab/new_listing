package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"listing-tracker/internal/market"
)

// consoleMu keeps progress lines and log records from interleaving.
var consoleMu sync.Mutex

// line prints one progress line: time, then the text.
func line(format string, args ...any) {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// label pads exchange names to the same console width (Korean letters are
// two columns wide): "빗썸" + two spaces lines up with "업비트".
func label(exchange market.Exchange) string {
	name := exchange.Label()
	if exchange == market.Bithumb {
		return name + "  "
	}
	return name
}

func banner(version, url, lookback string) {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	rule := strings.Repeat("=", 48)
	fmt.Println(rule)
	fmt.Printf("  빗썸·업비트 신규 상장 추적기   %s\n", version)
	fmt.Println(rule)
	fmt.Printf("  화면 주소     %s\n", url)
	fmt.Printf("  찾는 범위     최근 %s 상장, 상장 후 24시간\n", lookback)
	fmt.Printf("  상장 공지     업비트 공지 목록 · 빗썸 공지 API\n")
	fmt.Printf("  종료          Ctrl+C 또는 창 닫기\n")
	fmt.Println(strings.Repeat("-", 48))
}

func usage() {
	name := filepath.Base(os.Args[0])
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, `빗썸·업비트 신규 상장 추적기

사용법
  %[1]s [옵션]

자주 쓰는 옵션
  -watch 코인들              지금부터 직접 추적할 코인, 쉼표로 구분 (예: BTC,ETH)
  -no-browser                켤 때 브라우저를 열지 않음

그 밖의 옵션
  -addr 주소                 화면 주소 (기본 127.0.0.1:8770)
  -lookback 기간             켤 때 찾을 상장 기간 (기본 168h = 7일)
  -registry-interval 간격    마켓 목록 확인 간격 (기본 5s)
  -upbit-rate 횟수           업비트 그룹별 초당 요청 상한 (기본 8, 최대 8)
                             같은 PC에서 다른 업비트 프로그램을 쓸 때 낮춥니다
  -verbose                   자세한 기록 출력
  -web 폴더                  화면 파일 폴더 (개발용)

예시
  %[1]s
  %[1]s -watch BTC
  %[1]s -no-browser -addr 127.0.0.1:9000
`, name)
}

// consoleHandler writes log records as "15:04:05  경고  message  key=value".
type consoleHandler struct {
	out   io.Writer
	level slog.Level
	attrs []slog.Attr
}

func newConsoleHandler(out io.Writer, level slog.Level) *consoleHandler {
	return &consoleHandler{out: out, level: level}
}

func (h *consoleHandler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }

func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	kind := "알림"
	switch {
	case r.Level >= slog.LevelError:
		kind = "오류"
	case r.Level >= slog.LevelWarn:
		kind = "경고"
	case r.Level < slog.LevelInfo:
		kind = "기록"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s", r.Time.Format("15:04:05"), kind, r.Message)
	write := func(a slog.Attr) bool {
		fmt.Fprintf(&b, "  %s=%v", a.Key, a.Value)
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	b.WriteByte('\n')
	consoleMu.Lock()
	defer consoleMu.Unlock()
	_, err := io.WriteString(h.out, b.String())
	return err
}

func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &next
}

func (h *consoleHandler) WithGroup(string) slog.Handler { return h }
