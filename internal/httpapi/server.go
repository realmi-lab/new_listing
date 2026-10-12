// Package httpapi serves the snapshot, a once-a-second event stream of it,
// and the screen.
package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"listing-tracker/internal/tracker"
)

const maxStreams = 64

type Server struct {
	tracker *tracker.Tracker
	feeds   func() any
	now     func() time.Time
	web     fs.FS

	// hosts, when set, are the only Host headers served. A loopback
	// listener accepts only its own names, so a web page that rebinds its
	// domain to 127.0.0.1 cannot read the API through the viewer's browser.
	hosts map[string]bool

	mu       sync.Mutex
	cached   []byte
	cachedAt time.Time
	streams  int
}

// LoopbackOnly limits requests to the loopback names of the given port.
func (s *Server) LoopbackOnly(port string) {
	s.hosts = map[string]bool{
		"127.0.0.1:" + port: true, "localhost:" + port: true, "[::1]:" + port: true,
	}
}

func New(t *tracker.Tracker, feeds func() any, now func() time.Time, web fs.FS) *Server {
	return &Server{tracker: t, feeds: feeds, now: now, web: web}
}

// snapshot is built at most every 500ms however many viewers ask.
func (s *Server) snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && time.Since(s.cachedAt) < 500*time.Millisecond {
		return s.cached
	}
	snap := s.tracker.Snapshot(s.now())
	if s.feeds != nil {
		snap.Feeds = s.feeds()
	}
	body, err := json.Marshal(snap)
	if err != nil {
		return []byte(`{"error":"snapshot"}`)
	}
	s.cached, s.cachedAt = body, time.Now()
	return body
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(s.snapshot())
	})
	mux.HandleFunc("GET /api/stream", s.stream)
	if s.web != nil {
		files := http.FileServerFS(s.web)
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if _, err := fs.Stat(s.web, path); err != nil {
				r.URL.Path = "/" // the screen routes itself
			}
			files.ServeHTTP(w, r)
		})
	} else {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("화면 파일이 이 실행 파일에 포함되지 않았습니다. /api/snapshot 으로 자료를 확인할 수 있습니다.\n"))
		})
	}
	return s.checkHost(securityHeaders(mux))
}

func (s *Server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.hosts != nil && !s.hosts[strings.ToLower(r.Host)] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// stream sends the snapshot once a second as server-sent events.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	if s.streams >= maxStreams {
		s.mu.Unlock()
		http.Error(w, "too many viewers", http.StatusServiceUnavailable)
		return
	}
	s.streams++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.streams--
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := w.Write([]byte("data: ")); err != nil {
			return
		}
		_, _ = w.Write(s.snapshot())
		if _, err := w.Write([]byte("\n\n")); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
