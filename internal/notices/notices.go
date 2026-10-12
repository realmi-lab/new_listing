// Package notices follows listing announcements: Bithumb's official notice
// API, Upbit's announcement list, and Upbit's announcement WebSocket when an
// API key is configured. A notice announces a listing; the market list and
// the first candle confirm it.
package notices

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"listing-tracker/internal/candles"
	"listing-tracker/internal/market"
)

const (
	BithumbURL = "https://api.bithumb.com/v1/notices?count=20"
	// Upbit's web announcement list. It is not part of the documented Open
	// API and may change; the WebSocket below is the documented source.
	UpbitListURL      = "https://api-manager.upbit.com/api/v1/announcements?os=web&page=1&per_page=20&category=trade"
	UpbitWebSocketURL = "wss://api.upbit.com/websocket/v1/private"
)

type Notice struct {
	Exchange    market.Exchange `json:"exchange"`
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	URL         string          `json:"url"`
	Category    string          `json:"category"`
	PublishedAt time.Time       `json:"published_at"`
	Source      string          `json:"source"` // LIST or WEBSOCKET
	Listing     bool            `json:"listing"`
	KRW         bool            `json:"krw"`
	Symbols     []string        `json:"symbols"`
}

var (
	listingWords = []string{"신규 거래지원", "신규 거래 지원", "디지털 자산 추가", "마켓 추가", "마켓추가", "거래지원 개시", "신규 상장"}
	excludeWords = []string{"종료", "유의", "이벤트", "에어드랍", "기념", "연기", "취소", "중단", "중지"}
	symbolRE     = regexp.MustCompile(`\(([A-Z0-9]{1,15})\)`)
	quoteTokens  = map[string]bool{"KRW": true, "BTC": true, "USDT": true}
)

// Classify marks listing announcements and reads the asset symbols written
// in parentheses, e.g. "리저브라이트(RSR) KRW, USDT 마켓 디지털 자산 추가".
func Classify(n Notice) Notice {
	title := n.Title
	n.Listing = false
	for _, word := range listingWords {
		if strings.Contains(title, word) {
			n.Listing = true
			break
		}
	}
	for _, word := range excludeWords {
		if strings.Contains(title, word) {
			n.Listing = false
		}
	}
	n.Symbols = nil
	seen := map[string]bool{}
	for _, match := range symbolRE.FindAllStringSubmatch(title, -1) {
		symbol := match[1]
		if quoteTokens[symbol] || seen[symbol] {
			continue
		}
		seen[symbol] = true
		n.Symbols = append(n.Symbols, symbol)
	}
	mentionsOther := strings.Contains(title, "BTC 마켓") || strings.Contains(title, "USDT 마켓")
	n.KRW = strings.Contains(title, "원화") || strings.Contains(title, "KRW") || !mentionsOther
	return n
}

// ParseBithumb reads GET https://api.bithumb.com/v1/notices.
func ParseBithumb(payload []byte) ([]Notice, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("Bithumb notices are not a JSON array")
	}
	var rows []struct {
		Categories  []string `json:"categories"`
		Title       string   `json:"title"`
		URL         string   `json:"pc_url"`
		PublishedAt string   `json:"published_at"`
	}
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		return nil, err
	}
	result := make([]Notice, 0, len(rows))
	for _, r := range rows {
		if strings.TrimSpace(r.URL) == "" || strings.TrimSpace(r.Title) == "" {
			continue
		}
		published, _ := time.ParseInLocation("2006-01-02 15:04:05", r.PublishedAt, candles.KST)
		result = append(result, Classify(Notice{
			Exchange: market.Bithumb, ID: r.URL, Title: strings.TrimSpace(r.Title), URL: r.URL,
			Category: strings.Join(r.Categories, ", "), PublishedAt: published, Source: "LIST",
		}))
	}
	return result, nil
}

// ParseUpbitList reads Upbit's web announcement list.
func ParseUpbitList(payload []byte) ([]Notice, error) {
	var root struct {
		Success bool `json:"success"`
		Data    struct {
			Notices []struct {
				ID            json.Number `json:"id"`
				UUID          string      `json:"uuid"`
				Title         string      `json:"title"`
				Category      string      `json:"category"`
				FirstListedAt string      `json:"first_listed_at"`
				ListedAt      string      `json:"listed_at"`
			} `json:"notices"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, err
	}
	if !root.Success {
		return nil, errors.New("Upbit announcement list reported failure")
	}
	result := make([]Notice, 0, len(root.Data.Notices))
	for _, r := range root.Data.Notices {
		page := r.ID.String()
		// The WebSocket identifies an announcement by uuid; use it here too
		// so both sources name the same notice the same way.
		id := strings.TrimSpace(r.UUID)
		if id == "" {
			id = page
		}
		if id == "" || strings.TrimSpace(r.Title) == "" {
			continue
		}
		published := parseTime(r.FirstListedAt, r.ListedAt)
		result = append(result, Classify(Notice{
			Exchange: market.Upbit, ID: id, Title: strings.TrimSpace(r.Title),
			URL: "https://upbit.com/service_center/notice?id=" + page, Category: r.Category,
			PublishedAt: published, Source: "LIST",
		}))
	}
	return result, nil
}

// ParseUpbitAnnouncement reads one announcement WebSocket frame. Other
// frames return ok=false; an error frame returns its message as err.
func ParseUpbitAnnouncement(payload []byte) (Notice, bool, error) {
	var frame struct {
		Type          string          `json:"type"`
		SimpleType    string          `json:"ty"`
		UUID          string          `json:"uuid"`
		Title         string          `json:"title"`
		Category      string          `json:"category"`
		URL           string          `json:"url"`
		FirstListedAt string          `json:"first_listed_at"`
		ListedAt      string          `json:"listed_at"`
		Error         json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		return Notice{}, false, nil
	}
	if len(frame.Error) > 0 && string(frame.Error) != "null" {
		return Notice{}, false, fmt.Errorf("Upbit WebSocket error: %s", strings.TrimSpace(string(frame.Error)))
	}
	if frame.Type != "announcement" && frame.SimpleType != "announcement" {
		return Notice{}, false, nil
	}
	if frame.UUID == "" || frame.Title == "" {
		return Notice{}, false, nil
	}
	url := frame.URL
	if url == "" {
		url = "https://upbit.com/service_center/notice"
	}
	return Classify(Notice{
		Exchange: market.Upbit, ID: frame.UUID, Title: strings.TrimSpace(frame.Title), URL: url,
		Category: frame.Category, PublishedAt: parseTime(frame.FirstListedAt, frame.ListedAt), Source: "WEBSOCKET",
	}), true, nil
}

func parseTime(values ...string) time.Time {
	for _, value := range values {
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Key identifies a notice across the list and WebSocket sources.
func Key(n Notice) string { return string(n.Exchange) + "/" + n.ID }
