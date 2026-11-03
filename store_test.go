package main

import (
	"context"
	"os"
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
