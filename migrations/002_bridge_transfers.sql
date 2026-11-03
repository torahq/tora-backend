create table if not exists bridge_transfers (
  id bigserial primary key,
  address text not null,
  leg text not null,
  token text not null,
  amount text not null,
  tx_hash text not null unique,
  status text not null default 'pending',
  detail text,
  claim jsonb,
  ticket text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index if not exists bridge_transfers_address on bridge_transfers (address, created_at desc);
