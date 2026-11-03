package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	pulse    *Pulse
	featured *Market
	tokens   []elyTokenRow
	pools    []elyPool
	compare  []json.RawMessage
	err      error
	pg, rd   bool
}

func (f fakeStore) SavePulse(context.Context, Pulse) error        { return nil }
func (f fakeStore) UpsertMarkets(context.Context, []Market) error { return nil }
func (f fakeStore) Health(context.Context) (bool, bool)           { return f.pg, f.rd }
func (f fakeStore) UpsertElysium(context.Context, []elyPool, []elyToken) error {
	return nil
}
func (f fakeStore) ElysiumCursor(context.Context, string) (int64, bool, error) {
	return 0, false, f.err
}
func (f fakeStore) SetElysiumCursor(context.Context, string, int64) error { return nil }
func (f fakeStore) KnownTokens(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (f fakeStore) ElysiumTokens(context.Context, string, int) ([]elyTokenRow, error) {
	return f.tokens, f.err
}
func (f fakeStore) ElysiumPools(context.Context, []string) ([]elyPool, error) {
	return f.pools, f.err
}
func (f fakeStore) SaveCompare(context.Context, []cmpQuestion) error { return f.err }
func (f fakeStore) Compare(context.Context) ([]json.RawMessage, error) {
	return f.compare, f.err
}
func (f fakeStore) Pulse(context.Context) (Pulse, bool, error) {
	if f.pulse == nil {
		return Pulse{}, false, f.err
	}
	return *f.pulse, true, nil
}
func (f fakeStore) Featured(context.Context) (Market, bool, error) {
	if f.featured == nil {
		return Market{}, false, f.err
	}
	return *f.featured, true, nil
}

var clock = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func get(t *testing.T, s Store, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	routes(s, func() time.Time { return clock }).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: body is not JSON: %q", path, rec.Body.String())
	}
	return rec.Code, body
}

func getRaw(t *testing.T, s Store, path string) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	routes(s, func() time.Time { return clock }).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.Bytes()
}

func TestPulseEndpoint(t *testing.T) {
	code, body := get(t, fakeStore{}, "/v1/pulse")
	if code != 503 || body["error"] != "pulse not ready" {
		t.Errorf("empty: %d %v", code, body)
	}
	code, _ = get(t, fakeStore{err: errors.New("redis down")}, "/v1/pulse")
	if code != 503 {
		t.Errorf("store error: %d", code)
	}
	fresh := Pulse{Volume24h: 5, Markets: 2, UpdatedAt: clock.Add(-10 * time.Second)}
	code, body = get(t, fakeStore{pulse: &fresh}, "/v1/pulse")
	if code != 200 || body["volume24h"] != 5.0 || body["markets"] != 2.0 || body["stale"] != false {
		t.Errorf("fresh: %d %v", code, body)
	}
	old := Pulse{UpdatedAt: clock.Add(-time.Minute)}
	_, body = get(t, fakeStore{pulse: &old}, "/v1/pulse")
	if body["stale"] != true {
		t.Errorf("old pulse must be stale: %v", body)
	}
}

func TestFeaturedEndpoint(t *testing.T) {
	code, body := get(t, fakeStore{}, "/v1/predict/featured")
	if code != 404 || body["error"] != "no featured market" {
		t.Errorf("none: %d %v", code, body)
	}
	code, _ = get(t, fakeStore{err: errors.New("pg down")}, "/v1/predict/featured")
	if code != 503 {
		t.Errorf("store error: %d", code)
	}
	closes := clock.Add(5 * time.Hour)
	m := Market{Venue: "hyperliquid", VenueID: "7", Title: "BTC above 80,000?", Category: "crypto", ClosesAt: &closes, Yes: 0.42, URL: "/predict", UpdatedAt: clock}
	code, body = get(t, fakeStore{featured: &m}, "/v1/predict/featured")
	if code != 200 || body["venueId"] != "7" || body["yes"] != 0.42 || body["closesAt"] != "2026-11-04T17:00:00Z" {
		t.Errorf("found: %d %v", code, body)
	}
}

func TestCompareEndpoint(t *testing.T) {
	code, body := getRaw(t, fakeStore{}, "/v1/predict/compare")
	if code != 200 || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("empty: %d %q", code, body)
	}

	payloads := []json.RawMessage{
		json.RawMessage(`{"question":566,"title":"Fenerbahce vs Liverpool"}`),
		json.RawMessage(`{"question":198,"title":"English Premier League 2026/2027 winner"}`),
	}
	code, body = getRaw(t, fakeStore{compare: payloads}, "/v1/predict/compare")
	var got []map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not a JSON array: %q", body)
	}
	if code != 200 || len(got) != 2 || got[0]["question"] != 566.0 || got[1]["question"] != 198.0 {
		t.Errorf("payloads: %d %v", code, got)
	}

	code, body = getRaw(t, fakeStore{err: errors.New("pg down")}, "/v1/predict/compare")
	var e map[string]any
	if err := json.Unmarshal(body, &e); err != nil || code != 503 || e["error"] != "compare unavailable" {
		t.Errorf("store error: %d %v", code, e)
	}
}

func TestHealthz(t *testing.T) {
	code, body := get(t, fakeStore{pg: true, rd: true}, "/healthz")
	if code != 200 || body["ok"] != true {
		t.Errorf("healthy: %d %v", code, body)
	}
	code, body = get(t, fakeStore{pg: true}, "/healthz")
	if code != 503 || body["ok"] != false || body["redis"] != false {
		t.Errorf("redis down: %d %v", code, body)
	}
}
