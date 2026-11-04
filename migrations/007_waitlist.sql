-- Landing waitlist: Tora opens with Elysium mainnet. Written by the app (src/lib/waitlist.ts).
create table if not exists waitlist (
  email text primary key,
  created_at timestamptz not null default now()
);
