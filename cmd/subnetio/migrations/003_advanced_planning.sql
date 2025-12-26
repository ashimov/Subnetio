-- Copyright (c) 2025 Berik Ashimov

ALTER TABLE pools ADD COLUMN family TEXT NOT NULL DEFAULT 'ipv4';
ALTER TABLE pools ADD COLUMN tier TEXT;
ALTER TABLE pools ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;

ALTER TABLE segments ADD COLUMN prefix_v6 INTEGER;
ALTER TABLE segments ADD COLUMN cidr_v6 TEXT;

ALTER TABLE segment_meta ADD COLUMN gateway_v6 TEXT;
ALTER TABLE segment_meta ADD COLUMN pool_tier TEXT;

ALTER TABLE project_rules ADD COLUMN pool_strategy TEXT NOT NULL DEFAULT 'spillover';
ALTER TABLE project_rules ADD COLUMN pool_tier_fallback INTEGER NOT NULL DEFAULT 1;

ALTER TABLE project_meta ADD COLUMN growth_rate REAL;
ALTER TABLE project_meta ADD COLUMN growth_months INTEGER;

CREATE TABLE IF NOT EXISTS deployed_configs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL,
  template TEXT NOT NULL,
  scope_key TEXT NOT NULL,
  content TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(project_id) REFERENCES projects(id),
  UNIQUE(project_id, template, scope_key)
);
