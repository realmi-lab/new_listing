package restclient

import (
	"net/http"
	"net/url"
	"testing"
)

func TestRedirectsStayOnExchangeDomains(t *testing.T) {
	check := NewHTTPClient(0).CheckRedirect
	for raw, allowed := range map[string]bool{
		"https://feed-api.bithumb.com/v1/notices": true,
		"https://api.upbit.com/v1/market/all":     true,
		"https://api.korbit.co.kr/v2/candles":     true,
		"http://feed-api.bithumb.com/v1/notices":  false,
		"https://bithumb.com.evil.example/":       false,
		"https://evilbithumb.com/":                false,
		"https://example.com/":                    false,
	} {
		u, _ := url.Parse(raw)
		err := check(&http.Request{URL: u}, nil)
		if (err == nil) != allowed {
			t.Errorf("%s: allowed=%v err=%v", raw, allowed, err)
		}
	}
}

func TestRemainingPerSecond(t *testing.T) {
	if n, ok := remainingPerSecond("group=ticker; min=1800; sec=9"); !ok || n != 9 {
		t.Fatalf("n=%d ok=%v", n, ok)
	}
	if _, ok := remainingPerSecond(""); ok {
		t.Fatal("empty header parsed")
	}
}
