create table if not exists elysium_tokens (
  address text primary key,
  symbol text not null,
  name text not null,
  decimals int not null
);
create table if not exists elysium_pools (
  venue text not null,
  kind text not null,
  pool text not null,
  token0 text not null,
  token1 text not null,
  fee int not null,
  tick_spacing int not null default 0,
  hooks text not null default '0x0000000000000000000000000000000000000000',
  block bigint not null,
  primary key (venue, pool)
);
create index if not exists elysium_pools_tokens on elysium_pools (token0, token1);
create table if not exists elysium_cursor (venue text primary key, block bigint not null);
