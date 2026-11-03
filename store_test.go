package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func testDB(t *testing.T) db {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from markets where venue = 'test'`); err != nil {
		t.Fatal(err)
	}
	opt, err := redis.ParseURL(os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { rdb.Close() })
	return db{pool, rdb}
}

func TestFeaturedPicksSoonestFreshUndecidedCrypto(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	if _, err := d.pg.Exec(ctx, `delete from markets`); err != nil {
		t.Fatal(err)
	}
	at := func(h time.Duration) *time.Time { v := time.Now().Add(h); return &v }
	ms := []Market{
		{Venue: "test", VenueID: "too-soon", Title: "a", Category: "crypto", ClosesAt: at(time.Hour), Yes: 0.5, URL: "/predict"},
		{Venue: "test", VenueID: "decided", Title: "b", Category: "crypto", ClosesAt: at(3 * time.Hour), Yes: 0.97, URL: "/predict"},
		{Venue: "test", VenueID: "sports", Title: "c", Category: "sports", ClosesAt: at(3 * time.Hour), Yes: 0.5, URL: "/predict"},
		{Venue: "test", VenueID: "pick", Title: "d", Category: "crypto", ClosesAt: at(5 * time.Hour), Yes: 0.4, URL: "/predict"},
		{Venue: "test", VenueID: "later", Title: "e", Category: "crypto", ClosesAt: at(9 * time.Hour), Yes: 0.4, URL: "/predict"},
	}
	if err := d.UpsertMarkets(ctx, ms); err != nil {
		t.Fatal(err)
	}
	m, ok, err := d.Featured(ctx)
	if err != nil || !ok || m.VenueID != "pick" {
		t.Fatalf("featured = %+v %v %v, want pick", m, ok, err)
	}

	// no crypto candidate left: fall back to any category
	if _, err := d.pg.Exec(ctx, `delete from markets where category = 'crypto'`); err != nil {
		t.Fatal(err)
	}
	m, ok, err = d.Featured(ctx)
	if err != nil || !ok || m.VenueID != "sports" {
		t.Fatalf("fallback = %+v %v %v, want sports", m, ok, err)
	}
}

func TestPulseRoundTrip(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	p := Pulse{Volume24h: 1, OpenInterest: 2, Markets: 3, Gainer: &Mover{Coin: "BTC", Change: 0.1}, UpdatedAt: time.Now().UTC().Truncate(time.Second)}
	if err := d.SavePulse(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.Pulse(ctx)
	if err != nil || !ok || got.Markets != 3 || got.Gainer.Coin != "BTC" || !got.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatalf("got %+v %v %v", got, ok, err)
	}
}

func TestElysiumPoolsAndTokens(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	for _, q := range []string{`delete from elysium_pools`, `delete from elysium_tokens`, `delete from elysium_cursor`} {
		if _, err := d.pg.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	const (
		usdc  = "0x7d29d8047b905000459c0e80c34a26ceedcb47b2"
		whype = "0xcd57f65c2b0e5881cfc2e609f7cd53b746e1f234"
		aaa   = "0x1111111111111111111111111111111111111111"
		bbb   = "0x2222222222222222222222222222222222222222"
		hook  = "0xda605da70bff4b32a9b379c614a6d9e4a09baaec"
	)
	pools := []elyPool{
		{Venue: "test-v2", Kind: "v2", Pool: "0x3333333333333333333333333333333333333333", Token0: usdc, Token1: aaa, Fee: 3000, Hooks: zeroAddr, Block: 1},
		{Venue: "test-v3", Kind: "v3", Pool: "0x4444444444444444444444444444444444444444", Token0: aaa, Token1: bbb, Fee: 500, TickSpacing: 10, Hooks: zeroAddr, Block: 2},
		{Venue: "test-v4", Kind: "v4", Pool: "0x5555555555555555555555555555555555555555", Token0: zeroAddr, Token1: whype, Fee: 3000, TickSpacing: 60, Hooks: hook, Block: 3},
	}
	tokens := []elyToken{
		{Address: usdc, Symbol: "USDC", Name: "USD Coin", Decimals: 6},
		{Address: whype, Symbol: "WHYPE", Name: "Wrapped HYPE", Decimals: 18},
		{Address: aaa, Symbol: "AAA", Name: "Alpha", Decimals: 18},
		{Address: bbb, Symbol: "BBB", Name: "Beta", Decimals: 18},
	}
	if err := d.UpsertElysium(ctx, pools, tokens); err != nil {
		t.Fatal(err)
	}

	// set filtering: only pools holding both requested tokens
	cases := []struct {
		tokens []string
		want   string
	}{
		{[]string{usdc, aaa}, "test-v2"},
		{[]string{aaa, bbb}, "test-v3"},
		{[]string{zeroAddr, whype}, "test-v4"},
		{[]string{usdc, bbb}, ""},
	}
	for _, c := range cases {
		got, err := d.ElysiumPools(ctx, c.tokens)
		if err != nil {
			t.Fatal(err)
		}
		if c.want == "" {
			if len(got) != 0 {
				t.Errorf("%v: got %d pools, want none", c.tokens, len(got))
			}
			continue
		}
		if len(got) != 1 || got[0].Venue != c.want || got[0].Block == 0 {
			t.Errorf("%v: got %+v, want %s", c.tokens, got, c.want)
		}
	}

	// token ordering: HYPE, USDC, WHYPE first, then by pool count
	rows, err := d.ElysiumTokens(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		address string
		symbol  string
		pools   int
	}{
		{zeroAddr, "HYPE", 1},
		{usdc, "USDC", 1},
		{whype, "WHYPE", 1},
		{aaa, "AAA", 2},
		{bbb, "BBB", 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("tokens = %+v, want %d rows", rows, len(want))
	}
	for i, w := range want {
		if rows[i].Address != w.address || rows[i].Symbol != w.symbol || rows[i].Pools != w.pools {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}

	// q matches symbol or name case-insensitively, or an exact address
	rows, err = d.ElysiumTokens(ctx, "bbb", 10)
	if err != nil || len(rows) != 1 || rows[0].Symbol != "BBB" {
		t.Errorf("symbol query: %+v %v", rows, err)
	}
	rows, err = d.ElysiumTokens(ctx, "wrapped hype", 10)
	if err != nil || len(rows) != 1 || rows[0].Symbol != "WHYPE" {
		t.Errorf("name query: %+v %v", rows, err)
	}
	rows, err = d.ElysiumTokens(ctx, strings.ToUpper(aaa), 10)
	if err != nil || len(rows) != 1 || rows[0].Symbol != "AAA" {
		t.Errorf("address query: %+v %v", rows, err)
	}
	rows, err = d.ElysiumTokens(ctx, "", 3)
	if err != nil || len(rows) != 3 {
		t.Errorf("limit: %+v %v", rows, err)
	}
}

func TestCompareReplacesRowsAndOrders(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	if _, err := d.pg.Exec(ctx, `delete from predict_compare`); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	early, late := now.Add(time.Hour), now.Add(2*time.Hour)
	if err := d.SaveCompare(ctx, []cmpQuestion{
		{Question: 1, Title: "late match", StartsAt: &late, UpdatedAt: now},
		{Question: 2, Title: "season winner", UpdatedAt: now},
		{Question: 3, Title: "early match", StartsAt: &early, UpdatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := d.Compare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	var got []cmpQuestion
	for _, r := range rows {
		var q cmpQuestion
		if err := json.Unmarshal(r, &q); err != nil {
			t.Fatalf("payload is not a cmpQuestion: %v", err)
		}
		got = append(got, q)
	}
	// soonest start first, a question without one last
	if got[0].Question != 3 || got[1].Question != 1 || got[2].Question != 2 {
		t.Errorf("order = %d %d %d, want 3 1 2", got[0].Question, got[1].Question, got[2].Question)
	}
	if got[1].StartsAt == nil || !got[1].StartsAt.Equal(late) || got[2].StartsAt != nil {
		t.Errorf("startsAt round trip = %v %v", got[1].StartsAt, got[2].StartsAt)
	}

	// a second save replaces every row, including the ones that are gone
	if err := d.SaveCompare(ctx, []cmpQuestion{{Question: 9, Title: "only", StartsAt: &early, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	rows, err = d.Compare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var only cmpQuestion
	if len(rows) != 1 || json.Unmarshal(rows[0], &only) != nil || only.Question != 9 {
		t.Errorf("after replace = %s", rows)
	}
}

func TestElysiumCursorAndKnownTokens(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	for _, q := range []string{`delete from elysium_tokens`, `delete from elysium_cursor`} {
		if _, err := d.pg.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := d.ElysiumCursor(ctx, "kalos"); err != nil || ok {
		t.Fatalf("unknown cursor = %v %v", ok, err)
	}
	if err := d.SetElysiumCursor(ctx, "kalos", 7_645_302); err != nil {
		t.Fatal(err)
	}
	if err := d.SetElysiumCursor(ctx, "kalos", 7_645_400); err != nil {
		t.Fatal(err)
	}
	block, ok, err := d.ElysiumCursor(ctx, "kalos")
	if err != nil || !ok || block != 7_645_400 {
		t.Fatalf("cursor = %d %v %v", block, ok, err)
	}

	known, err := d.KnownTokens(ctx, []string{"0x1111111111111111111111111111111111111111"})
	if err != nil || len(known) != 0 {
		t.Fatalf("known empty = %v %v", known, err)
	}
	if err := d.UpsertElysium(ctx, nil, []elyToken{{Address: "0x1111111111111111111111111111111111111111", Symbol: "AAA", Name: "Alpha", Decimals: 18}}); err != nil {
		t.Fatal(err)
	}
	known, err = d.KnownTokens(ctx, []string{"0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"})
	if err != nil || !known["0x1111111111111111111111111111111111111111"] || len(known) != 1 {
		t.Fatalf("known = %v %v", known, err)
	}
}
