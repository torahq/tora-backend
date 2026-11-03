create table if not exists perps_compare (
  asset text primary key,
  hl_volume double precision not null,
  payload jsonb not null,
  updated_at timestamptz not null
);
