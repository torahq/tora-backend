create table if not exists markets (
  venue text not null,
  venue_id text not null,
  title text not null,
  category text not null,
  closes_at timestamptz,
  yes_price double precision,
  url text not null,
  updated_at timestamptz not null default now(),
  primary key (venue, venue_id)
);
create index if not exists markets_category_closes on markets (category, closes_at);
