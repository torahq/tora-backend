package main

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Perps compare: every Hyperliquid core perp next to the same market on Aster, Lighter and GMX. The
// API contract lives in docs/superpowers/specs/2026-11-04-perps-aggregator-design.md.

type perpQuote struct {
	Venue         string   `json:"venue"` // hyperliquid, aster, lighter, gmx
	Mark          float64  `json:"mark"`
	FundingHourly float64  `json:"fundingHourly"` // decimal per hour, positive = longs pay
	FundingAPR    float64  `json:"fundingApr"`    // FundingHourly * 24 * 365
	OIUSD         *float64 `json:"oiUsd"`
	VolumeUSD     *float64 `json:"volumeUsd"`
	URL           string   `json:"url"`
}

type perpRow struct {
	Asset     string      `json:"asset"`
	UpdatedAt time.Time   `json:"updatedAt"` // always UTC
	Venues    []perpQuote `json:"venues"`
	hlVolume  float64     // for ordering, not encoded
}

type hlPerp struct {
	Name                         string
	Mark, Funding, OI, DayNtlVlm float64
} // core metaAndAssetCtxs, OI in coins
type asterRow struct {
	Symbol            string
	Mark, LastFunding float64
	IntervalH         int
	QuoteVolume       float64
	OI                *float64
} // OI in base units, nil when not fetched
type lighterRow struct {
	Symbol                      string
	Mark, DayQuoteVolume, OIUSD float64
	Active                      bool
	Funding8h                   *float64
}
type gmxPair struct {
	Base                                        string
	LastPrice, FundingPct, OpenInterest, Volume float64
} // one pool of /pairs

// markGuard drops a venue quote whose mark is more than 10% away from Hyperliquid's. Several venues
// use the same short ticker for unrelated assets (S, CC, MET, LIT, MON).
const markGuard = 0.10

// skipAsset drops Hyperliquid names whose price unit differs from the other venues: prefixed memes
// (kPEPE, 1000BONK, 1MBABYDOGE) quote 1000 units per token elsewhere.
func skipAsset(name string) bool {
	if strings.HasPrefix(name, "1000") || strings.HasPrefix(name, "1M") {
		return true
	}
	return len(name) > 1 && name[0] == 'k' && name[1] >= 'A' && name[1] <= 'Z'
}

func fundingAPR(hourly float64) float64 { return hourly * 24 * 365 }

// asterQuote normalizes one Aster market. lastFundingRate is settled per intervalHours (8 when the
// fundingInfo call has no row), so dividing gives the hourly decimal.
func asterQuote(r asterRow) perpQuote {
	hours := r.IntervalH
	if hours == 0 {
		hours = 8
	}
	hourly := r.LastFunding / float64(hours)
	volume := r.QuoteVolume
	q := perpQuote{
		Venue:         "aster",
		Mark:          r.Mark,
		FundingHourly: hourly,
		FundingAPR:    fundingAPR(hourly),
		VolumeUSD:     &volume,
		URL:           "https://www.asterdex.com/en/trade/pro/futures/" + r.Symbol,
	}
	if r.OI != nil {
		oi := *r.OI * r.Mark
		q.OIUSD = &oi
	}
	return q
}

// lighterQuote normalizes one Lighter market. Its funding_rates row is the 8h-equivalent decimal,
// while funding is paid hourly.
func lighterQuote(r lighterRow) (perpQuote, bool) {
	if !r.Active {
		return perpQuote{}, false
	}
	var hourly float64
	if r.Funding8h != nil {
		hourly = *r.Funding8h / 8
	}
	oi, volume := r.OIUSD, r.DayQuoteVolume
	return perpQuote{
		Venue:         "lighter",
		Mark:          r.Mark,
		FundingHourly: hourly,
		FundingAPR:    fundingAPR(hourly),
		OIUSD:         &oi,
		VolumeUSD:     &volume,
		URL:           "https://app.lighter.xyz/trade/" + r.Symbol,
	}, true
}

// gmxQuote merges an asset's pools: price and funding come from the deepest pool (the most open
// interest), while OI and volume are summed. funding_rate is percent per hour.
func gmxQuote(pools []gmxPair) perpQuote {
	deepest := pools[0]
	oi, volume := 0.0, 0.0
	for _, p := range pools {
		if p.OpenInterest > deepest.OpenInterest {
			deepest = p
		}
		oi += p.OpenInterest
		volume += p.Volume
	}
	hourly := deepest.FundingPct / 100
	return perpQuote{
		Venue:         "gmx",
		Mark:          deepest.LastPrice,
		FundingHourly: hourly,
		FundingAPR:    fundingAPR(hourly),
		OIUSD:         &oi,
		VolumeUSD:     &volume,
		URL:           "https://app.gmx.io/trade",
	}
}

// buildPerpsCompare turns the four already-normalized venue inputs into one row per asset that
// Hyperliquid core lists and at least one other venue also lists. Input maps are keyed by base
// symbol. Rows are ordered by Hyperliquid 24h volume, the venue the terminal is built on.
func buildPerpsCompare(hl []hlPerp, aster map[string]asterRow, lighter map[string]lighterRow, gmx map[string][]gmxPair, now time.Time) []perpRow {
	rows := make([]perpRow, 0, len(hl))
	for _, p := range hl {
		if skipAsset(p.Name) {
			continue
		}
		mark := p.Mark
		oi, volume := p.OI*mark, p.DayNtlVlm
		row := perpRow{
			Asset:     p.Name,
			UpdatedAt: now.UTC(),
			Venues: []perpQuote{{
				Venue:         "hyperliquid",
				Mark:          mark,
				FundingHourly: p.Funding,
				FundingAPR:    fundingAPR(p.Funding),
				OIUSD:         &oi,
				VolumeUSD:     &volume,
				URL:           "https://app.hyperliquid.xyz/trade/" + p.Name,
			}},
			hlVolume: p.DayNtlVlm,
		}

		var others []perpQuote
		if a, ok := aster[p.Name]; ok {
			others = append(others, asterQuote(a))
		}
		if l, ok := lighter[p.Name]; ok {
			if q, ok := lighterQuote(l); ok {
				others = append(others, q)
			}
		}
		if pools := gmx[p.Name]; len(pools) > 0 {
			others = append(others, gmxQuote(pools))
		}
		for _, q := range others {
			if q.Mark == 0 || math.Abs(q.Mark/mark-1) > markGuard {
				continue
			}
			row.Venues = append(row.Venues, q)
		}
		if len(row.Venues) < 2 {
			continue
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].hlVolume > rows[j].hlVolume })
	return rows
}

// asterOITargets names the Aster symbols ("BTCUSDT") worth one openInterest call each: the n deepest
// Hyperliquid perps by 24h volume that Aster lists. Aster has no bulk open interest, so the fetcher
// asks only for these.
func asterOITargets(hl []hlPerp, aster map[string]asterRow, n int) []string {
	sorted := append([]hlPerp(nil), hl...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].DayNtlVlm > sorted[j].DayNtlVlm })
	var out []string
	for _, p := range sorted {
		if len(out) >= n {
			break
		}
		if skipAsset(p.Name) {
			continue
		}
		r, ok := aster[p.Name]
		if !ok {
			continue
		}
		out = append(out, r.Symbol)
	}
	return out
}
