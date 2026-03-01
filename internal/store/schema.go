package store

const schemaSQL = `
CREATE TABLE IF NOT EXISTS collection_runs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at  TEXT NOT NULL,
	finished_at TEXT,
	status      TEXT NOT NULL DEFAULT 'running',
	duration_ms INTEGER,
	summary     TEXT
);

CREATE TABLE IF NOT EXISTS collection_sources (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id        INTEGER NOT NULL REFERENCES collection_runs(id),
	source        TEXT NOT NULL,
	source_type   TEXT NOT NULL DEFAULT 'native',
	status        TEXT NOT NULL DEFAULT 'running',
	started_at    TEXT NOT NULL,
	finished_at   TEXT,
	error_message TEXT,
	item_count    INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS hosts (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id           INTEGER NOT NULL REFERENCES collection_runs(id),
	name             TEXT NOT NULL,
	sources          TEXT NOT NULL,
	host_type        TEXT NOT NULL,
	status           TEXT NOT NULL,
	zone             TEXT,
	tailscale_ip     TEXT,
	local_ip         TEXT,
	public_ipv4      TEXT,
	cpu_cores        INTEGER,
	memory_mb        INTEGER,
	disk_gb          REAL,
	application      TEXT,
	category         TEXT,
	monthly_cost_eur REAL,
	details          TEXT
);

CREATE TABLE IF NOT EXISTS services (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id         INTEGER NOT NULL REFERENCES collection_runs(id),
	host_name      TEXT NOT NULL,
	source         TEXT NOT NULL,
	service_name   TEXT NOT NULL,
	container_name TEXT,
	image          TEXT,
	stack_name     TEXT,
	details        TEXT
);

CREATE TABLE IF NOT EXISTS networks (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id       INTEGER NOT NULL REFERENCES collection_runs(id),
	name         TEXT NOT NULL,
	vlan_id      INTEGER,
	subnet       TEXT,
	gateway      TEXT,
	dhcp_enabled INTEGER DEFAULT 0,
	details      TEXT
);

CREATE TABLE IF NOT EXISTS firewalls (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id     INTEGER NOT NULL REFERENCES collection_runs(id),
	name       TEXT NOT NULL,
	rules      TEXT,
	applied_to TEXT
);

CREATE TABLE IF NOT EXISTS tailscale_acl (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id     INTEGER NOT NULL REFERENCES collection_runs(id),
	acl_policy TEXT
);

CREATE TABLE IF NOT EXISTS tailscale_dns (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id            INTEGER NOT NULL REFERENCES collection_runs(id),
	nameservers       TEXT,
	search_paths      TEXT,
	magic_dns_enabled INTEGER DEFAULT 0,
	split_dns         TEXT
);

CREATE TABLE IF NOT EXISTS tailscale_routes (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id      INTEGER NOT NULL REFERENCES collection_runs(id),
	device_name TEXT NOT NULL,
	advertised  TEXT,
	enabled     TEXT
);

CREATE TABLE IF NOT EXISTS tailscale_keys (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id       INTEGER NOT NULL REFERENCES collection_runs(id),
	key_id       TEXT NOT NULL,
	description  TEXT,
	created_at   TEXT NOT NULL,
	expires_at   TEXT NOT NULL,
	capabilities TEXT
);

CREATE TABLE IF NOT EXISTS plugin_metrics (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id      INTEGER NOT NULL REFERENCES collection_runs(id),
	plugin_name TEXT NOT NULL,
	metrics     TEXT
);

CREATE TABLE IF NOT EXISTS findings (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id       INTEGER NOT NULL REFERENCES collection_runs(id),
	source       TEXT NOT NULL,
	finding_type TEXT NOT NULL,
	severity     TEXT NOT NULL,
	host_name    TEXT,
	message      TEXT NOT NULL,
	details      TEXT
);

CREATE TABLE IF NOT EXISTS config (
	key   TEXT PRIMARY KEY,
	value TEXT
);

CREATE INDEX IF NOT EXISTS idx_hosts_run_id ON hosts(run_id);
CREATE INDEX IF NOT EXISTS idx_hosts_name ON hosts(name);
CREATE INDEX IF NOT EXISTS idx_hosts_sources ON hosts(sources);
CREATE INDEX IF NOT EXISTS idx_services_run_id ON services(run_id);
CREATE INDEX IF NOT EXISTS idx_services_host ON services(host_name);
CREATE INDEX IF NOT EXISTS idx_findings_run_id ON findings(run_id);
CREATE INDEX IF NOT EXISTS idx_collection_sources_run ON collection_sources(run_id);
`
