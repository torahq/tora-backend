package main

import (
	"testing"
	"time"
)

func TestSkipAsset(t *testing.T) {
	for _, name := range []string{"kPEPE", "1000BONK", "1MBABYDOGE"} {
		if !skipAsset(name) {
			t.Errorf("%s must be skipped", name)
		}
	}
	for _, name := range []string{"KAITO", "BTC", "HYPE", "k"} {
		if skipAsset(name) {
			t.Errorf("%s must be kept", name)
		}
	}
}

func TestAsterQuote(t *testing.T) {
	q := asterQuote(asterRow{Symbol: "BTCUSDT", Mark: 77774.74, LastFunding: 0.0001, IntervalH: 8, QuoteVolume: 275_870_000})
	if q.Venue != "aster" || !approx(q.Mark, 77774.74) {
		t.Errorf("quote = %+v", q)
	}
	if !approx(q.FundingHourly, 1.25e-5) || !approx(q.FundingAPR, 0.1095) {
		t.Errorf("funding = %v %v, want 1.25e-5 0.1095", q.FundingHourly, q.FundingAPR)
	}
	if q.OIUSD != nil {
		t.Errorf("OI must stay null when not fetched: %v", *q.OIUSD)
	}
	if q.VolumeUSD == nil || !approx(*q.VolumeUSD, 275_870_000) {
		t.Errorf("volume = %v", q.VolumeUSD)
	}
	if q.URL != "https://www.asterdex.com/en/trade/pro/futures/BTCUSDT" {
		t.Errorf("url = %s", q.URL)
	}

	// a 4h interval settles four times as often, so the hourly rate is smaller
	if q := asterQuote(asterRow{LastFunding: 0.0001, IntervalH: 4}); !approx(q.FundingHourly, 2.5e-5) {
		t.Errorf("4h funding = %v, want 2.5e-5", q.FundingHourly)
	}
	// an interval the fundingInfo call did not report is treated as 8h
	if q := asterQuote(asterRow{LastFunding: 0.0001}); !approx(q.FundingHourly, 1.25e-5) {
		t.Errorf("default interval funding = %v, want 1.25e-5", q.FundingHourly)
	}

	oi := 6307.744
	q = asterQuote(asterRow{Symbol: "BTCUSDT", Mark: 77774.74, OI: &oi})
	if q.OIUSD == nil || !approx(*q.OIUSD, 6307.744*77774.74) {
		t.Errorf("OI in USD = %v, want base units times mark", q.OIUSD)
	}
}

func TestLighterQuote(t *testing.T) {
	funding := 9.6e-5
	q, ok := lighterQuote(lighterRow{Symbol: "BTC", Mark: 77773.8, DayQuoteVolume: 922_600_000, OIUSD: 349_300_000, Active: true, Funding8h: &funding})
	if !ok || q.Venue != "lighter" || !approx(q.Mark, 77773.8) {
		t.Fatalf("quote = %+v %v", q, ok)
	}
	if !approx(q.FundingHourly, 1.2e-5) || !approx(q.FundingAPR, 0.10512) {
		t.Errorf("funding = %v %v, want 1.2e-5 0.10512", q.FundingHourly, q.FundingAPR)
	}
	if q.OIUSD == nil || !approx(*q.OIUSD, 349_300_000) || q.VolumeUSD == nil || !approx(*q.VolumeUSD, 922_600_000) {
		t.Errorf("oi/volume = %v %v", q.OIUSD, q.VolumeUSD)
	}
	if q.URL != "https://app.lighter.xyz/trade/BTC" {
		t.Errorf("url = %s", q.URL)
	}

	if _, ok := lighterQuote(lighterRow{Symbol: "DEAD", Mark: 1, Active: false}); ok {
		t.Error("an inactive market must be dropped")
	}
	if q, ok := lighterQuote(lighterRow{Symbol: "BTC", Mark: 1, Active: true}); !ok || q.FundingHourly != 0 {
		t.Errorf("missing funding = %+v %v, want a zero rate", q, ok)
	}
}

func TestGMXQuote(t *testing.T) {
	q := gmxQuote([]gmxPair{
		{Base: "BTC", LastPrice: 77694.16, FundingPct: 0.0022806, OpenInterest: 17_380_000, Volume: 8_420_000},
		{Base: "BTC", LastPrice: 77600, FundingPct: 0.001, OpenInterest: 4_900_000, Volume: 1_180_000},
	})
	if q.Venue != "gmx" || !approx(q.Mark, 77694.16) {
		t.Errorf("mark = %v, want the deepest pool's price", q.Mark)
	}
	if !approx(q.FundingHourly, 2.2806e-5) {
		t.Errorf("funding = %v, want the deepest pool's 2.2806e-5", q.FundingHourly)
	}
	if q.OIUSD == nil || !approx(*q.OIUSD, 22_280_000) {
		t.Errorf("oi = %v, want the pool sum", q.OIUSD)
	}
	if q.VolumeUSD == nil || !approx(*q.VolumeUSD, 9_600_000) {
		t.Errorf("volume = %v, want the pool sum", q.VolumeUSD)
	}
	if q.URL != "https://app.gmx.io/trade" {
		t.Errorf("url = %s", q.URL)
	}
}

func TestBuildPerpsCompare(t *testing.T) {
	now := time.Date(2026, 11, 4, 5, 40, 0, 0, time.UTC)
	hl := []hlPerp{
		{Name: "BTC", Mark: 77759, Funding: 1.25e-5, OI: 41763.5, DayNtlVlm: 2_059_000_000},
		{Name: "kPEPE", Mark: 0.0043, Funding: 1e-5, OI: 1, DayNtlVlm: 900_000_000},
	}
	funding := 9.6e-5
	rows := buildPerpsCompare(hl,
		map[string]asterRow{"BTC": {Symbol: "BTCUSDT", Mark: 77774.74, LastFunding: 0.0001, IntervalH: 8, QuoteVolume: 275_870_000, OI: ptr(6307.744)}},
		map[string]lighterRow{"BTC": {Symbol: "BTC", Mark: 77773.8, DayQuoteVolume: 922_600_000, OIUSD: 349_300_000, Active: true, Funding8h: &funding}},
		map[string][]gmxPair{"BTC": {{Base: "BTC", LastPrice: 77694.16, FundingPct: 0.0022806, OpenInterest: 17_380_000, Volume: 8_420_000}}},
		now)

	if len(rows) != 1 || rows[0].Asset != "BTC" {
		t.Fatalf("rows = %+v, want only BTC (kPEPE is skipped)", rows)
	}
	r := rows[0]
	if !r.UpdatedAt.Equal(now) || r.UpdatedAt.Location() != time.UTC {
		t.Errorf("updatedAt = %v, want %v in UTC", r.UpdatedAt, now)
	}
	want := []string{"hyperliquid", "aster", "lighter", "gmx"}
	if len(r.Venues) != len(want) {
		t.Fatalf("venues = %+v, want %v", r.Venues, want)
	}
	for i, venue := range want {
		if r.Venues[i].Venue != venue {
			t.Errorf("venue %d = %s, want %s", i, r.Venues[i].Venue, venue)
		}
	}
	if hl := r.Venues[0]; hl.OIUSD == nil || !approx(*hl.OIUSD, 41763.5*77759) || !approx(hl.FundingAPR, 1.25e-5*24*365) {
		t.Errorf("hl quote = %+v", hl)
	}
	if r.Venues[1].OIUSD == nil || !approx(*r.Venues[1].OIUSD, 6307.744*77774.74) {
		t.Errorf("aster oi = %v", r.Venues[1].OIUSD)
	}
	if r.hlVolume != 2_059_000_000 {
		t.Errorf("hlVolume = %v", r.hlVolume)
	}
}

func TestBuildPerpsCompareMarkGuard(t *testing.T) {
	hl := []hlPerp{{Name: "S", Mark: 0.3, Funding: 1e-5, OI: 1000, DayNtlVlm: 50_000_000}}
	// the same ticker is a different asset on Lighter, priced far away
	rows := buildPerpsCompare(hl,
		map[string]asterRow{"S": {Symbol: "SUSDT", Mark: 0.301, LastFunding: 0.0001, IntervalH: 8}},
		map[string]lighterRow{"S": {Symbol: "S", Mark: 0.5, Active: true}},
		nil, time.Now())
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the aster quote kept", rows)
	}
	if venues := rows[0].Venues; len(venues) != 2 || venues[1].Venue != "aster" {
		t.Errorf("venues = %+v, want the 0.5 lighter quote dropped and aster kept", venues)
	}

	// no quote inside the guard leaves the asset out entirely
	rows = buildPerpsCompare(hl, nil, map[string]lighterRow{"S": {Symbol: "S", Mark: 0.5, Active: true}}, nil, time.Now())
	if len(rows) != 0 {
		t.Errorf("rows = %+v, want none", rows)
	}
}

func TestBuildPerpsCompareOrdersByVolume(t *testing.T) {
	hl := []hlPerp{
		{Name: "BTC", Mark: 100, Funding: 1e-5, OI: 10, DayNtlVlm: 100},
		{Name: "ETH", Mark: 100, Funding: 1e-5, OI: 10, DayNtlVlm: 900}, // Hyperliquid only
		{Name: "SOL", Mark: 100, Funding: 1e-5, OI: 10, DayNtlVlm: 300},
	}
	aster := map[string]asterRow{
		"BTC": {Symbol: "BTCUSDT", Mark: 100, LastFunding: 0.0001},
		"SOL": {Symbol: "SOLUSDT", Mark: 100, LastFunding: 0.0001},
	}
	rows := buildPerpsCompare(hl, aster, nil, nil, time.Now())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want BTC and SOL (ETH has no other venue)", rows)
	}
	if rows[0].Asset != "SOL" || rows[1].Asset != "BTC" {
		t.Errorf("order = %s %s, want SOL BTC", rows[0].Asset, rows[1].Asset)
	}
}

func TestAsterOITargets(t *testing.T) {
	hl := []hlPerp{
		{Name: "BTC", DayNtlVlm: 900},
		{Name: "ETH", DayNtlVlm: 500}, // Aster does not list it
		{Name: "SOL", DayNtlVlm: 400},
		{Name: "kPEPE", DayNtlVlm: 300}, // prefixed, never compared
		{Name: "DOGE", DayNtlVlm: 200},
	}
	aster := map[string]asterRow{
		"BTC":  {Symbol: "BTCUSDT"},
		"SOL":  {Symbol: "SOLUSDT"},
		"DOGE": {Symbol: "DOGEUSDT"},
	}
	got := asterOITargets(hl, aster, 10)
	want := []string{"BTCUSDT", "SOLUSDT", "DOGEUSDT"}
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("targets = %v, want %v", got, want)
		}
	}
	if got := asterOITargets(hl, aster, 1); len(got) != 1 || got[0] != "BTCUSDT" {
		t.Errorf("n=1 targets = %v, want BTCUSDT", got)
	}
	if got := asterOITargets(hl, nil, 3); len(got) != 0 {
		t.Errorf("no listings = %v, want none", got)
	}
}
