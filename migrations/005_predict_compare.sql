create table if not exists predict_compare (
  question int primary key,
  starts_at timestamptz,
  title text not null,
  payload jsonb not null,
  updated_at timestamptz not null
);
