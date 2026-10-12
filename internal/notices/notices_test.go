package notices

import (
	"reflect"
	"strings"
	"testing"
)

func TestClassifyListingTitles(t *testing.T) {
	cases := []struct {
		title   string
		listing bool
		krw     bool
		symbols []string
	}{
		{"리저브라이트(RSR) KRW, USDT 마켓 디지털 자산 추가", true, true, []string{"RSR"}},
		{"카이아(KAIA) 신규 거래지원 안내 (KRW, BTC, USDT 마켓) (KAIA/BTC 초기 매수 제한 안내)", true, true, []string{"KAIA"}},
		{"제스트프로토콜(ZEST) 원화 마켓 추가", true, true, []string{"ZEST"}},
		{"아베(AAVE) BTC 마켓 디지털 자산 추가", true, false, []string{"AAVE"}},
		{"[이벤트] 총 4억원 상당, 제스트프로토콜(ZEST) 원화마켓 추가 기념 에어드랍 이벤트", false, true, []string{"ZEST"}},
		{"블라스트(BLAST) 거래 유의 종목 지정 안내", false, true, []string{"BLAST"}},
		{"디와이디엑스(DYDX) 입출금 일시 중지 안내 (10/12 오후 7시~)", false, true, []string{"DYDX"}},
		{"에이(AAA), 비(BBB) 원화 마켓 추가", true, true, []string{"AAA", "BBB"}},
	}
	for _, c := range cases {
		got := Classify(Notice{Title: c.title})
		if got.Listing != c.listing || got.KRW != c.krw || !reflect.DeepEqual(got.Symbols, c.symbols) {
			t.Errorf("%s: listing=%v krw=%v symbols=%v", c.title, got.Listing, got.KRW, got.Symbols)
		}
	}
}

func TestParseBithumbNotices(t *testing.T) {
	rows, err := ParseBithumb([]byte(`[{"categories":["마켓 추가"],"title":"제스트프로토콜(ZEST) 원화 마켓 추가","pc_url":"https://feed.bithumb.com/notice/1655235","published_at":"2026-10-12 11:41:50","modified_at":"2026-10-12 13:25:04"}]`))
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	n := rows[0]
	if !n.Listing || n.ID != "https://feed.bithumb.com/notice/1655235" || n.PublishedAt.UTC().Hour() != 2 || n.Category != "마켓 추가" {
		t.Fatalf("notice %+v", n)
	}
	if _, err := ParseBithumb([]byte(`<html>302</html>`)); err == nil {
		t.Fatal("HTML accepted")
	}
}

func TestParseUpbitListUsesTheUUID(t *testing.T) {
	list, err := ParseUpbitList([]byte(`{"success":true,"data":{"notices":[{"listed_at":"2026-10-12T13:54:09+09:00","first_listed_at":"2026-10-12T13:54:09+09:00","id":6651,"uuid":"1933802018","title":"리저브라이트(RSR) KRW, USDT 마켓 디지털 자산 추가","category":"거래"}]}}`))
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	n := list[0]
	if n.ID != "1933802018" || !strings.Contains(n.URL, "id=6651") || !n.Listing || n.PublishedAt.IsZero() {
		t.Fatalf("notice %+v", n)
	}
	if _, err := ParseUpbitList([]byte(`{"success":false}`)); err == nil {
		t.Fatal("failed answer accepted")
	}
}
