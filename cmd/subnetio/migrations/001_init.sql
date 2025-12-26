CREATE TABLE IF NOT EXISTS sites (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS pools (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  site_id INTEGER NOT NULL,
  cidr TEXT NOT NULL,
  FOREIGN KEY(site_id) REFERENCES sites(id)
);

CREATE TABLE IF NOT EXISTS segments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  site_id INTEGER NOT NULL,
  vrf TEXT NOT NULL,
  vlan INTEGER NOT NULL,
  name TEXT NOT NULL,
  hosts INTEGER,
  prefix INTEGER,
  cidr TEXT,
  locked INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY(site_id) REFERENCES sites(id)
);

CREATE TABLE IF NOT EXISTS projects (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  description TEXT
);

CREATE TABLE IF NOT EXISTS project_sites (
  project_id INTEGER NOT NULL,
  site_id INTEGER NOT NULL UNIQUE,
  FOREIGN KEY(project_id) REFERENCES projects(id),
  FOREIGN KEY(site_id) REFERENCES sites(id)
);

CREATE TABLE IF NOT EXISTS site_meta (
  site_id INTEGER PRIMARY KEY,
  region TEXT,
  dns TEXT,
  ntp TEXT,
  gateway_policy TEXT,
  reserved_ranges TEXT,
  dhcp_search TEXT,
  dhcp_lease_time INTEGER,
  dhcp_renew_time INTEGER,
  dhcp_rebind_time INTEGER,
  dhcp_boot_file TEXT,
  dhcp_next_server TEXT,
  dhcp_vendor_options TEXT,
  FOREIGN KEY(site_id) REFERENCES sites(id)
);

CREATE TABLE IF NOT EXISTS segment_meta (
  segment_id INTEGER PRIMARY KEY,
  dhcp_enabled INTEGER NOT NULL DEFAULT 0,
  dhcp_range TEXT,
  dhcp_reservations TEXT,
  gateway TEXT,
  notes TEXT,
  tags TEXT,
  FOREIGN KEY(segment_id) REFERENCES segments(id)
);

CREATE TABLE IF NOT EXISTS project_rules (
  project_id INTEGER PRIMARY KEY,
  vlan_scope TEXT NOT NULL DEFAULT 'site_vrf',
  require_in_pool INTEGER NOT NULL DEFAULT 1,
  allow_reserved_overlap INTEGER NOT NULL DEFAULT 0,
  oversize_threshold INTEGER NOT NULL DEFAULT 50,
  FOREIGN KEY(project_id) REFERENCES projects(id)
);

CREATE TABLE IF NOT EXISTS project_meta (
  project_id INTEGER PRIMARY KEY,
  domain_name TEXT,
  dhcp_search TEXT,
  dhcp_lease_time INTEGER,
  dhcp_renew_time INTEGER,
  dhcp_rebind_time INTEGER,
  dhcp_boot_file TEXT,
  dhcp_next_server TEXT,
  dhcp_vendor_options TEXT,
  FOREIGN KEY(project_id) REFERENCES projects(id)
);
