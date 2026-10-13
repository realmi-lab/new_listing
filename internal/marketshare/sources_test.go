package marketshare

import (
	"encoding/json"
	"testing"
)

func TestEstimatePriceIgnoresAnOutlierTrade(t *testing.T) {
	// Korbit ONE_KRW 2026-10-12 18:00: o/h 10,000,000, l 172.2, c 174.
	if p := estimatePrice(json.Number("10000000"), json.Number("172.2"), json.Number("174")); p != 174 {
		t.Fatalf("outlier hour price %v, want the close", p)
	}
	if p := estimatePrice(json.Number("1349"), json.Number("1347"), json.Number("1347")); p < 1347 || p > 1349 {
		t.Fatalf("normal hour price %v", p)
	}
}
