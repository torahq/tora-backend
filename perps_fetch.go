package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// perpsVenues holds the three read-only comparison venue bases. Reads are public and unauthenticated.
type perpsVenues struct {
	Aster   string
	Lighter string
	GMX     string
	Client  *http.Client
}

// getJSON reuses the predict fetcher's HTTP shape: status check, then a JSON decode.
func (v perpsVenues) getJSON(ctx context.Context, u string, out any) error {
	return venues{Client: v.Client}.getJSON(ctx, u, out)
}

// num reads a venue number that may arrive as a JSON number or as a decimal string. Aster sends
// strings, GMX sends numbers and Lighter mixes both.
type num float64

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("%q: %w", b, err)
	}
	*n = num(v)
	return nil
}

type hlUniverse struct {
	Name       string `json:"name"`
	IsDelisted bool   `json:"isDelisted"`
}

// hlCtx is one core metaAndAssetCtxs row. Every number arrives as a string.
type hlCtx struct {
	Funding      num `json:"funding"`
	OpenInterest num `json:"openInterest"`
	MarkPx       num `json:"markPx"`
	DayNtlVlm    num `json:"dayNtlVlm"`
}

// fetchHLPerps reads Hyperliquid mainnet core markets, without the delisted rows the universe keeps.
func fetchHLPerps(ctx context.Context, hl HL) ([]hlPerp, error) {
	var parts []json.RawMessage
	if err := hl.post(ctx, map[string]string{"type": "metaAndAssetCtxs"}, &parts); err != nil {
		return nil, err
	}
	if len(parts) != 2 {
		return nil, fmt.Errorf("metaAndAssetCtxs: want 2 parts, got %d", len(parts))
	}
	var meta struct {
		Universe []hlUniverse `json:"universe"`
	}
	var ctxs []hlCtx
	if err := json.Unmarshal(parts[0], &meta); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(parts[1], &ctxs); err != nil {
		return nil, err
	}
	if len(ctxs) != len(meta.Universe) {
		return nil, fmt.Errorf("metaAndAssetCtxs: %d universe rows, %d ctxs", len(meta.Universe), len(ctxs))
	}
	out := make([]hlPerp, 0, len(ctxs))
	for i, c := range ctxs {
		if meta.Universe[i].IsDelisted {
			continue
		}
		out = append(out, hlPerp{
			Name:      meta.Universe[i].Name,
			Mark:      float64(c.MarkPx),
			Funding:   float64(c.Funding),
			OI:        float64(c.OpenInterest),
			DayNtlVlm: float64(c.DayNtlVlm),
		})
	}
	return out, nil
}

type asterPremium struct {
	Symbol          string `json:"symbol"`
	MarkPrice       num    `json:"markPrice"`
	LastFundingRate num    `json:"lastFundingRate"`
}

type asterTicker struct {
	Symbol      string `json:"symbol"`
	QuoteVolume num    `json:"quoteVolume"`
}

type asterFundingInfo struct {
	Symbol               string `json:"symbol"`
	FundingIntervalHours num    `json:"fundingIntervalHours"`
}

type asterOI struct {
	OpenInterest num `json:"openInterest"`
}

// asterBase maps a USDT-quoted Aster symbol to the base name the venues share ("BTCUSDT" -> "BTC").
// USD1 and USDC markets are skipped: keyed by base they would collide with the USDT one.
func asterBase(symbol string) (string, bool) {
	base := strings.TrimSuffix(symbol, "USDT")
	if base == symbol || base == "" {
		return "", false
	}
	return base, true
}

// aster reads the three bulk calls and then one openInterest call per top asset. A failing open
// interest call leaves that cell null by design; every other error fails the run.
func (v perpsVenues) aster(ctx context.Context, hl []hlPerp) (map[string]asterRow, error) {
	var premium []asterPremium
	if err := v.getJSON(ctx, v.Aster+"/fapi/v1/premiumIndex", &premium); err != nil {
		return nil, err
	}
	var tickers []asterTicker
	if err := v.getJSON(ctx, v.Aster+"/fapi/v1/ticker/24hr", &tickers); err != nil {
		return nil, err
	}
	var infos []asterFundingInfo
	if err := v.getJSON(ctx, v.Aster+"/fapi/v1/fundingInfo", &infos); err != nil {
		return nil, err
	}

	rows := make(map[string]asterRow, len(premium))
	for _, p := range premium {
		base, ok := asterBase(p.Symbol)
		if !ok {
			continue
		}
		rows[base] = asterRow{Symbol: p.Symbol, Mark: float64(p.MarkPrice), LastFunding: float64(p.LastFundingRate)}
	}
	for _, t := range tickers {
		base, ok := asterBase(t.Symbol)
		if !ok {
			continue
		}
		if r, ok := rows[base]; ok {
			r.QuoteVolume = float64(t.QuoteVolume)
			rows[base] = r
		}
	}
	for _, f := range infos {
		base, ok := asterBase(f.Symbol)
		if !ok {
			continue
		}
		if r, ok := rows[base]; ok {
			r.IntervalH = int(f.FundingIntervalHours)
			rows[base] = r
		}
	}

	// Aster has no bulk open interest and a single-symbol call costs about 16 request weight.
	p := pacer{gap: 150 * time.Millisecond}
	for _, symbol := range asterOITargets(hl, rows, 30) {
		p.wait()
		var oi asterOI
		if err := v.getJSON(ctx, v.Aster+"/fapi/v1/openInterest?symbol="+url.QueryEscape(symbol), &oi); err != nil {
			continue
		}
		base, ok := asterBase(symbol)
		if !ok {
			continue
		}
		r := rows[base]
		value := float64(oi.OpenInterest)
		r.OI = &value
		rows[base] = r
	}
	return rows, nil
}

type lighterDetails struct {
	OrderBookDetails []lighterDetail `json:"order_book_details"`
}

type lighterDetail struct {
	Symbol                string `json:"symbol"`
	Status                string `json:"status"`
	MarketType            string `json:"market_type"`
	MarkPrice             num    `json:"mark_price"`
	DailyQuoteTokenVolume num    `json:"daily_quote_token_volume"`
	OpenInterestUSD       num    `json:"open_interest_usd"`
}

type lighterFunding struct {
	MarketID int    `json:"market_id"`
	Exchange string `json:"exchange"`
	Symbol   string `json:"symbol"`
	Rate     num    `json:"rate"`
}

// lighter reads the perp markets and the hourly funding rows. funding_rates carries four exchanges;
// only the lighter rows are this venue's own rate.
func (v perpsVenues) lighter(ctx context.Context) (map[string]lighterRow, error) {
	var details lighterDetails
	if err := v.getJSON(ctx, v.Lighter+"/api/v1/orderBookDetails", &details); err != nil {
		return nil, err
	}
	var rates struct {
		FundingRates []lighterFunding `json:"funding_rates"`
	}
	if err := v.getJSON(ctx, v.Lighter+"/api/v1/funding-rates", &rates); err != nil {
		return nil, err
	}
	funding := make(map[string]float64, len(rates.FundingRates))
	for _, r := range rates.FundingRates {
		if r.Exchange == "lighter" {
			funding[r.Symbol] = float64(r.Rate)
		}
	}

	rows := make(map[string]lighterRow, len(details.OrderBookDetails))
	for _, d := range details.OrderBookDetails {
		if d.MarketType != "perp" {
			continue
		}
		row := lighterRow{
			Symbol:         d.Symbol,
			Mark:           float64(d.MarkPrice),
			DayQuoteVolume: float64(d.DailyQuoteTokenVolume),
			OIUSD:          float64(d.OpenInterestUSD),
			Active:         d.Status == "active",
		}
		if rate, ok := funding[d.Symbol]; ok {
			row.Funding8h = &rate
		}
		rows[d.Symbol] = row
	}
	return rows, nil
}

type gmxPairJSON struct {
	BaseCurrency string `json:"base_currency"`
	LastPrice    num    `json:"last_price"`
	FundingRate  num    `json:"funding_rate"`
	OpenInterest num    `json:"open_interest"`
	TargetVolume num    `json:"target_volume"`
}

// gmx reads /pairs. funding_rate is percent per hour here; /markets/tickers has the opposite sign, so
// only /pairs is used.
func (v perpsVenues) gmx(ctx context.Context) (map[string][]gmxPair, error) {
	var pairs []gmxPairJSON
	if err := v.getJSON(ctx, v.GMX+"/pairs", &pairs); err != nil {
		return nil, err
	}
	rows := make(map[string][]gmxPair, len(pairs))
	for _, p := range pairs {
		if p.BaseCurrency == "" {
			continue
		}
		rows[p.BaseCurrency] = append(rows[p.BaseCurrency], gmxPair{
			Base:         p.BaseCurrency,
			LastPrice:    float64(p.LastPrice),
			FundingPct:   float64(p.FundingRate),
			OpenInterest: float64(p.OpenInterest),
			Volume:       float64(p.TargetVolume),
		})
	}
	return rows, nil
}

// fetchPerpsVenues calls the venues in sequence and returns the four build inputs. Any error fails
// the run, so the last good rows keep serving with their older updated_at.
func fetchPerpsVenues(ctx context.Context, hl HL, v perpsVenues) ([]hlPerp, map[string]asterRow, map[string]lighterRow, map[string][]gmxPair, error) {
	hlPerps, err := fetchHLPerps(ctx, hl)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	aster, err := v.aster(ctx, hlPerps)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	lighter, err := v.lighter(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	gmx, err := v.gmx(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return hlPerps, aster, lighter, gmx, nil
}

func pollPerpsCompare(hl HL, v perpsVenues, s Store) func(context.Context) error {
	return func(ctx context.Context) error {
		hlPerps, aster, lighter, gmx, err := fetchPerpsVenues(ctx, hl, v)
		if err != nil {
			return err
		}
		rows := buildPerpsCompare(hlPerps, aster, lighter, gmx, time.Now())
		if err := s.SavePerpsCompare(ctx, rows); err != nil {
			return err
		}
		slog.Info("perps compare built", "assets", len(rows))
		return nil
	}
}
