package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// venues holds the two comparison venue bases. Reads are public and unauthenticated.
type venues struct {
	Poly   string
	Kalshi string
	Client *http.Client
}

func (v venues) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	res, err := v.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", u, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// polyEvents lists open Polymarket events under a tag, following the keyset cursor for at most 10 pages.
func (v venues) polyEvents(ctx context.Context, tag string) ([]polyEvent, error) {
	var out []polyEvent
	cursor := ""
	for page := 0; page < 10; page++ {
		u := fmt.Sprintf("%s/events/keyset?tag_slug=%s&closed=false&limit=100", v.Poly, url.QueryEscape(tag))
		if cursor != "" {
			u += "&after_cursor=" + url.QueryEscape(cursor)
		}
		var res struct {
			Events     []polyEvent `json:"events"`
			NextCursor string      `json:"next_cursor"`
		}
		if err := v.getJSON(ctx, u, &res); err != nil {
			return nil, err
		}
		out = append(out, res.Events...)
		if cursor = res.NextCursor; cursor == "" {
			break
		}
	}
	return out, nil
}

// polyEventBySlug reads one Polymarket event by slug. Winner and Fed slugs carry a timestamp suffix
// and cannot be derived, so they are curated in compare.go.
func (v venues) polyEventBySlug(ctx context.Context, slug string) (polyEvent, error) {
	var res []polyEvent
	if err := v.getJSON(ctx, v.Poly+"/events?slug="+url.QueryEscape(slug), &res); err != nil {
		return polyEvent{}, err
	}
	if len(res) == 0 {
		return polyEvent{}, fmt.Errorf("polymarket event %s: not found", slug)
	}
	return res[0], nil
}

// kalshiEvents lists open Kalshi events of a series with nested markets, following at most 3 pages.
func (v venues) kalshiEvents(ctx context.Context, series string) ([]kalshiEventJSON, error) {
	var out []kalshiEventJSON
	cursor := ""
	for page := 0; page < 3; page++ {
		u := fmt.Sprintf("%s/events?series_ticker=%s&status=open&with_nested_markets=true&limit=200",
			v.Kalshi, url.QueryEscape(series))
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		var res struct {
			Events []kalshiEventJSON `json:"events"`
			Cursor string            `json:"cursor"`
		}
		if err := v.getJSON(ctx, u, &res); err != nil {
			return nil, err
		}
		out = append(out, res.Events...)
		if cursor = res.Cursor; cursor == "" {
			break
		}
	}
	return out, nil
}

// kalshiEventByTicker reads one Kalshi event. The nested markets sit inside "event" on this endpoint,
// with an empty sibling "markets" array, so both are accepted.
func (v venues) kalshiEventByTicker(ctx context.Context, ticker string) (kalshiEventJSON, error) {
	var res struct {
		Event   kalshiEventJSON `json:"event"`
		Markets []kalshiMarket  `json:"markets"`
	}
	if err := v.getJSON(ctx, v.Kalshi+"/events/"+url.PathEscape(ticker)+"?with_nested_markets=true", &res); err != nil {
		return kalshiEventJSON{}, err
	}
	if len(res.Event.Markets) == 0 {
		res.Event.Markets = res.Markets
	}
	return res.Event, nil
}

// pacer keeps Kalshi calls 250 ms apart. Their unauthenticated rate limit is undocumented, so stay low.
type pacer struct{ last time.Time }

func (p *pacer) wait() {
	if !p.last.IsZero() {
		if d := 250*time.Millisecond - time.Since(p.last); d > 0 {
			time.Sleep(d)
		}
	}
	p.last = time.Now()
}

// seriesOf keeps the Kalshi series part of an event ticker: "KXFEDDECISION-26DEC" -> "KXFEDDECISION".
func seriesOf(ticker string) string {
	if i := strings.Index(ticker, "-"); i > 0 {
		return ticker[:i]
	}
	return ticker
}

// fedMarkets keeps the Polymarket Fed markets that map to a HIP-4 bucket and labels them with it.
func fedMarkets(ms []venueMarket) []venueMarket {
	var out []venueMarket
	for _, m := range ms {
		b := fedBucket("polymarket", m.Label, "")
		if b == "" {
			continue
		}
		m.Label = b
		out = append(out, m)
	}
	return out
}

// fetchVenueEvents reads every event the compare build can use: matches discovered by tag and series,
// and the curated winner and Fed events. Any error fails the run, so the last good rows keep serving.
func fetchVenueEvents(ctx context.Context, v venues) ([]venueEvent, error) {
	var out []venueEvent
	var kp pacer

	for comp, src := range matchSources {
		evs, err := v.polyEvents(ctx, src.PolyTag)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if ev, ok := polyMatchEvent(e, comp); ok {
				out = append(out, ev)
			}
		}
		kp.wait()
		kevs, err := v.kalshiEvents(ctx, src.KalshiSeries)
		if err != nil {
			return nil, err
		}
		for _, e := range kevs {
			if ev := kalshiEvent(e, comp, src.KalshiSeries); len(ev.Markets) > 0 {
				out = append(out, ev)
			}
		}
	}

	for key, ref := range winnerEvents {
		comp := "winner|" + key
		pe, err := v.polyEventBySlug(ctx, ref.Poly)
		if err != nil {
			return nil, err
		}
		out = append(out, venueEvent{
			Venue:       "polymarket",
			Competition: comp,
			URL:         "https://polymarket.com/event/" + pe.Slug,
			Markets:     polyMarkets(pe),
		})
		kp.wait()
		ke, err := v.kalshiEventByTicker(ctx, ref.Kalshi)
		if err != nil {
			return nil, err
		}
		out = append(out, kalshiEvent(ke, comp, seriesOf(ref.Kalshi)))
	}

	for label, ref := range fedEvents {
		comp := "fed|" + label
		pe, err := v.polyEventBySlug(ctx, ref.Poly)
		if err != nil {
			return nil, err
		}
		out = append(out, venueEvent{
			Venue:       "polymarket",
			Competition: comp,
			URL:         "https://polymarket.com/event/" + pe.Slug,
			Markets:     fedMarkets(polyMarkets(pe)),
		})
		kp.wait()
		ke, err := v.kalshiEventByTicker(ctx, ref.Kalshi)
		if err != nil {
			return nil, err
		}
		out = append(out, kalshiEvent(ke, comp, seriesOf(ref.Kalshi)))
	}

	return out, nil
}

func pollCompare(hl HL, v venues, s Store) func(context.Context) error {
	return func(ctx context.Context) error {
		meta, err := hl.OutcomeMeta(ctx)
		if err != nil {
			return err
		}
		mids, err := hl.Mids(ctx)
		if err != nil {
			return err
		}
		events, err := fetchVenueEvents(ctx, v)
		if err != nil {
			return err
		}
		qs := buildCompare(meta, mids, events, time.Now())
		if err := s.SaveCompare(ctx, qs); err != nil {
			return err
		}
		slog.Info("compare built", "questions", len(qs))
		return nil
	}
}
