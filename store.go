package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Store is what the pollers write and the HTTP handlers read.
type Store interface {
	SavePulse(ctx context.Context, p Pulse) error
	Pulse(ctx context.Context) (Pulse, bool, error)
	UpsertMarkets(ctx context.Context, ms []Market) error
	Featured(ctx context.Context) (Market, bool, error)
	Health(ctx context.Context) (postgres, redis bool)
}

// db keeps hot snapshots in Redis and normalized markets in Postgres.
type db struct {
	pg  *pgxpool.Pool
	rdb *redis.Client
}

const pulseKey = "tora:pulse"

func (d db) SavePulse(ctx context.Context, p Pulse) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return d.rdb.Set(ctx, pulseKey, b, 5*time.Minute).Err()
}

func (d db) Pulse(ctx context.Context) (Pulse, bool, error) {
	b, err := d.rdb.Get(ctx, pulseKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return Pulse{}, false, nil
	}
	if err != nil {
		return Pulse{}, false, err
	}
	var p Pulse
	if err := json.Unmarshal(b, &p); err != nil {
		return Pulse{}, false, err
	}
	return p, true, nil
}

func (d db) UpsertMarkets(ctx context.Context, ms []Market) error {
	b := &pgx.Batch{}
	for _, m := range ms {
		b.Queue(`insert into markets (venue, venue_id, title, category, closes_at, yes_price, url, updated_at)
			values ($1, $2, $3, $4, $5, $6, $7, now())
			on conflict (venue, venue_id) do update set
			  title = excluded.title, category = excluded.category, closes_at = excluded.closes_at,
			  yes_price = excluded.yes_price, url = excluded.url, updated_at = now()`,
			m.Venue, m.VenueID, m.Title, m.Category, m.ClosesAt, m.Yes, m.URL)
	}
	return d.pg.SendBatch(ctx, b).Close()
}

// Featured is a fresh market closing soonest (at least 2h out) whose Yes price is not a foregone conclusion.
// Crypto first, then any category.
const featuredSQL = `select venue, venue_id, title, category, closes_at, yes_price, url, updated_at
from markets
where ($1::text = '' or category = $1::text)
  and closes_at > now() + interval '2 hours'
  and yes_price between 0.1 and 0.9
  and updated_at > now() - interval '5 minutes'
order by closes_at
limit 1`

func (d db) Featured(ctx context.Context) (Market, bool, error) {
	for _, cat := range []string{"crypto", ""} {
		var m Market
		err := d.pg.QueryRow(ctx, featuredSQL, cat).Scan(&m.Venue, &m.VenueID, &m.Title, &m.Category, &m.ClosesAt, &m.Yes, &m.URL, &m.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return Market{}, false, err
		}
		return m, true, nil
	}
	return Market{}, false, nil
}

func (d db) Health(ctx context.Context) (bool, bool) {
	return d.pg.Ping(ctx) == nil, d.rdb.Ping(ctx).Err() == nil
}
