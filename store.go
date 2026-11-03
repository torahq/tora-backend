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
	UpsertElysium(ctx context.Context, pools []elyPool, tokens []elyToken) error
	ElysiumCursor(ctx context.Context, venue string) (int64, bool, error)
	SetElysiumCursor(ctx context.Context, venue string, block int64) error
	KnownTokens(ctx context.Context, addrs []string) (map[string]bool, error)
	ElysiumTokens(ctx context.Context, q string, limit int) ([]elyTokenRow, error)
	ElysiumPools(ctx context.Context, tokens []string) ([]elyPool, error)
	SaveCompare(ctx context.Context, qs []cmpQuestion) error
	Compare(ctx context.Context) ([]json.RawMessage, error)
	SavePerpsCompare(ctx context.Context, rows []perpRow) error
	PerpsCompare(ctx context.Context, asset string) (json.RawMessage, bool, error)
	PerpsAssets(ctx context.Context) ([]string, time.Time, error)
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
		// pgx returns the session time zone; the API speaks UTC
		m.UpdatedAt = m.UpdatedAt.UTC()
		if m.ClosesAt != nil {
			c := m.ClosesAt.UTC()
			m.ClosesAt = &c
		}
		return m, true, nil
	}
	return Market{}, false, nil
}

// UpsertElysium writes pools and token metadata. Known token metadata is left as it is.
func (d db) UpsertElysium(ctx context.Context, pools []elyPool, tokens []elyToken) error {
	if len(pools) == 0 && len(tokens) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, p := range pools {
		b.Queue(`insert into elysium_pools (venue, kind, pool, token0, token1, fee, tick_spacing, hooks, block)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			on conflict (venue, pool) do update set
			  kind = excluded.kind, token0 = excluded.token0, token1 = excluded.token1,
			  fee = excluded.fee, tick_spacing = excluded.tick_spacing,
			  hooks = excluded.hooks, block = excluded.block`,
			p.Venue, p.Kind, p.Pool, p.Token0, p.Token1, p.Fee, p.TickSpacing, p.Hooks, p.Block)
	}
	for _, t := range tokens {
		b.Queue(`insert into elysium_tokens (address, symbol, name, decimals)
			values ($1, $2, $3, $4)
			on conflict (address) do nothing`,
			t.Address, t.Symbol, t.Name, t.Decimals)
	}
	return d.pg.SendBatch(ctx, b).Close()
}

func (d db) ElysiumCursor(ctx context.Context, venue string) (int64, bool, error) {
	var block int64
	err := d.pg.QueryRow(ctx, `select block from elysium_cursor where venue = $1`, venue).Scan(&block)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return block, true, nil
}

func (d db) SetElysiumCursor(ctx context.Context, venue string, block int64) error {
	_, err := d.pg.Exec(ctx, `insert into elysium_cursor (venue, block) values ($1, $2)
		on conflict (venue) do update set block = excluded.block`, venue, block)
	return err
}

func (d db) KnownTokens(ctx context.Context, addrs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(addrs) == 0 {
		return out, nil
	}
	rows, err := d.pg.Query(ctx, `select address from elysium_tokens where address = any($1::text[])`, addrs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out[a] = true
	}
	return out, rows.Err()
}

// elysiumTokensSQL orders native HYPE first, then USDC and WHYPE, then by how many pools hold a token.
// ponytail: counts every pool per request (about 300 ms at 176k pools); keep a pools column updated by the indexer if it grows past the app's 2 s timeout.
const elysiumTokensSQL = `with t as (
	select address, symbol, name, decimals from elysium_tokens
	where address <> '0x0000000000000000000000000000000000000000'
	union all
	select '0x0000000000000000000000000000000000000000', 'HYPE', 'Hyperliquid', 18
),
counts as (
	select address, count(*) as pools
	from (select token0 as address from elysium_pools
	      union all
	      select token1 as address from elysium_pools) x
	group by address
)
select t.address, t.symbol, t.name, t.decimals, coalesce(c.pools, 0)
from t left join counts c on c.address = t.address
where $1::text = '' or t.address = lower($1::text)
   or t.symbol ilike '%' || $1::text || '%' or t.name ilike '%' || $1::text || '%'
order by case t.address
	when '0x0000000000000000000000000000000000000000' then 0
	when '0x7d29d8047b905000459c0e80c34a26ceedcb47b2' then 1
	when '0xcd57f65c2b0e5881cfc2e609f7cd53b746e1f234' then 2
	else 3 end,
	coalesce(c.pools, 0) desc, t.symbol, t.address
limit $2`

func (d db) ElysiumTokens(ctx context.Context, q string, limit int) ([]elyTokenRow, error) {
	rows, err := d.pg.Query(ctx, elysiumTokensSQL, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []elyTokenRow
	for rows.Next() {
		var t elyTokenRow
		if err := rows.Scan(&t.Address, &t.Symbol, &t.Name, &t.Decimals, &t.Pools); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ElysiumPools returns pools that hold both of a token set. A caller asks about 2 to 10 tokens.
func (d db) ElysiumPools(ctx context.Context, tokens []string) ([]elyPool, error) {
	rows, err := d.pg.Query(ctx, `select venue, kind, pool, token0, token1, fee, tick_spacing, hooks, block
		from elysium_pools
		where token0 = any($1::text[]) and token1 = any($1::text[])
		order by venue, pool`, tokens)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []elyPool
	for rows.Next() {
		var p elyPool
		if err := rows.Scan(&p.Venue, &p.Kind, &p.Pool, &p.Token0, &p.Token1, &p.Fee, &p.TickSpacing, &p.Hooks, &p.Block); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveCompare replaces every comparison row in one transaction: a failed build keeps the old rows and
// their age stays visible in updated_at.
func (d db) SaveCompare(ctx context.Context, qs []cmpQuestion) error {
	tx, err := d.pg.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `delete from predict_compare`); err != nil {
		return err
	}
	if len(qs) > 0 {
		b := &pgx.Batch{}
		for _, q := range qs {
			payload, err := json.Marshal(q)
			if err != nil {
				return err
			}
			b.Queue(`insert into predict_compare (question, starts_at, title, payload, updated_at)
				values ($1, $2, $3, $4, $5)`,
				q.Question, q.StartsAt, q.Title, string(payload), q.UpdatedAt)
		}
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Compare returns the stored payloads in display order: soonest start first, questions without a start
// time last, then by title.
func (d db) Compare(ctx context.Context) ([]json.RawMessage, error) {
	rows, err := d.pg.Query(ctx, `select payload from predict_compare
		order by starts_at asc nulls last, title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}

// SavePerpsCompare replaces every perps comparison row in one transaction: a failed build keeps the
// old rows and their age stays visible in updated_at.
func (d db) SavePerpsCompare(ctx context.Context, rows []perpRow) error {
	tx, err := d.pg.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `delete from perps_compare`); err != nil {
		return err
	}
	if len(rows) > 0 {
		b := &pgx.Batch{}
		for _, r := range rows {
			payload, err := json.Marshal(r)
			if err != nil {
				return err
			}
			b.Queue(`insert into perps_compare (asset, hl_volume, payload, updated_at)
				values ($1, $2, $3, $4)`,
				r.Asset, r.hlVolume, string(payload), r.UpdatedAt)
		}
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// PerpsCompare returns one stored comparison. A missing asset means no other venue lists it.
func (d db) PerpsCompare(ctx context.Context, asset string) (json.RawMessage, bool, error) {
	var b []byte
	err := d.pg.QueryRow(ctx, `select payload from perps_compare where asset = $1`, asset).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(b), true, nil
}

// PerpsAssets lists the compared assets by Hyperliquid volume, plus the newest build time. An empty
// table returns no assets and a zero time.
func (d db) PerpsAssets(ctx context.Context) ([]string, time.Time, error) {
	rows, err := d.pg.Query(ctx, `select asset, updated_at from perps_compare order by hl_volume desc`)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	var assets []string
	var newest time.Time
	for rows.Next() {
		var asset string
		var at time.Time
		if err := rows.Scan(&asset, &at); err != nil {
			return nil, time.Time{}, err
		}
		assets = append(assets, asset)
		if at.After(newest) {
			newest = at.UTC()
		}
	}
	return assets, newest, rows.Err()
}

func (d db) Health(ctx context.Context) (bool, bool) {
	return d.pg.Ping(ctx) == nil, d.rdb.Ping(ctx).Err() == nil
}
