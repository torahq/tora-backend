alter table bridge_transfers alter column tx_hash drop not null;
alter table bridge_transfers add column if not exists deposit_address text unique;
alter table bridge_transfers add column if not exists source text;
