-- The gateway's state, on SQLite (sql_store.go). Times are unix
-- nanoseconds; JSON is TEXT. Every tenant-scoped table has `tenant`, '' for
-- the default tenant.

CREATE TABLE api_keys (
	id         TEXT PRIMARY KEY,
	hash       TEXT NOT NULL UNIQUE,
	user_name  TEXT NOT NULL,
	tenant     TEXT NOT NULL,
	scopes     TEXT NOT NULL,
	created    INTEGER NOT NULL,
	revoked    INTEGER NOT NULL DEFAULT 0,
	expires    INTEGER,
	email      TEXT,
	password   TEXT,
	last_login INTEGER,
	parent     TEXT REFERENCES api_keys(id) ON DELETE CASCADE
);
CREATE INDEX api_keys_user ON api_keys(user_name, tenant);
CREATE INDEX api_keys_parent ON api_keys(parent);
-- One live account per address, and per user.
CREATE UNIQUE INDEX api_keys_account_email ON api_keys(email) WHERE email IS NOT NULL AND revoked = 0 AND parent IS NULL;
CREATE UNIQUE INDEX api_keys_account_user ON api_keys(user_name, tenant) WHERE email IS NOT NULL AND revoked = 0 AND parent IS NULL;

CREATE TABLE invites (
	id              TEXT PRIMARY KEY,
	hash            TEXT NOT NULL UNIQUE,
	email           TEXT NOT NULL,
	user_name       TEXT NOT NULL,
	tenant          TEXT NOT NULL,
	scopes          TEXT NOT NULL,
	policy          TEXT NOT NULL DEFAULT '',
	expires_in_secs INTEGER NOT NULL DEFAULT 0,
	account_key     TEXT,
	created         INTEGER NOT NULL,
	expires         INTEGER NOT NULL
);
CREATE INDEX invites_email ON invites(email);

CREATE TABLE ssh_keys (
	id             TEXT PRIMARY KEY,
	user_name      TEXT NOT NULL,
	tenant         TEXT NOT NULL,
	fingerprint    TEXT NOT NULL,
	authorized_key TEXT NOT NULL,
	sandbox        TEXT NOT NULL DEFAULT '',
	created        INTEGER NOT NULL
);
CREATE INDEX ssh_keys_fingerprint ON ssh_keys(fingerprint);
CREATE INDEX ssh_keys_user ON ssh_keys(user_name);

CREATE TABLE ssh_tokens (
	hash      TEXT PRIMARY KEY,
	user_name TEXT NOT NULL,
	tenant    TEXT NOT NULL,
	sandbox   TEXT NOT NULL,
	expires   INTEGER NOT NULL
);
CREATE INDEX ssh_tokens_expires ON ssh_tokens(expires);

CREATE TABLE orgs (
	name_key          TEXT PRIMARY KEY,
	display_name      TEXT NOT NULL,
	created           INTEGER NOT NULL,
	created_by        TEXT NOT NULL,
	created_by_tenant TEXT NOT NULL DEFAULT ''
);

CREATE TABLE org_members (
	org         TEXT NOT NULL REFERENCES orgs(name_key) ON DELETE CASCADE,
	user_name   TEXT NOT NULL,
	user_tenant TEXT NOT NULL,
	role        TEXT NOT NULL,
	added       INTEGER NOT NULL,
	PRIMARY KEY (org, user_name, user_tenant)
);
-- One member of a name per organisation (nameTakenInOrg).
CREATE UNIQUE INDEX org_members_name ON org_members(org, user_name);
CREATE INDEX org_members_user ON org_members(user_name, user_tenant);

CREATE TABLE sandboxes (
	id                TEXT PRIMARY KEY,
	tenant            TEXT NOT NULL,
	user_name         TEXT NOT NULL,
	node              TEXT NOT NULL,
	cpus              REAL NOT NULL DEFAULT 0,
	memory_mb         INTEGER NOT NULL DEFAULT 0,
	disk_mb           INTEGER NOT NULL DEFAULT 0,
	recorded          INTEGER NOT NULL,
	name              TEXT NOT NULL DEFAULT '',
	image             TEXT NOT NULL DEFAULT '',
	state             TEXT NOT NULL DEFAULT '',
	labels            TEXT NOT NULL DEFAULT '{}',
	network           TEXT NOT NULL DEFAULT '{}',
	lifetime_secs     INTEGER NOT NULL DEFAULT 0,
	expires_at        INTEGER,
	idle_timeout_secs INTEGER NOT NULL DEFAULT 0,
	created_at        INTEGER,
	details_updated   INTEGER,
	terminated_at     INTEGER,
	terminated_reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sandboxes_tenant ON sandboxes(tenant, user_name);
CREATE INDEX sandboxes_node ON sandboxes(node);
CREATE INDEX sandboxes_terminated ON sandboxes(terminated_at);

CREATE TABLE volumes (
	name      TEXT PRIMARY KEY,
	tenant    TEXT NOT NULL,
	user_name TEXT NOT NULL,
	node      TEXT NOT NULL
);
CREATE INDEX volumes_tenant ON volumes(tenant, user_name);

CREATE TABLE snapshots (
	id        TEXT PRIMARY KEY,
	tenant    TEXT NOT NULL,
	user_name TEXT NOT NULL,
	node      TEXT NOT NULL
);
CREATE INDEX snapshots_node ON snapshots(node);

CREATE TABLE nodes (
	name       TEXT PRIMARY KEY,
	endpoint   TEXT NOT NULL,
	token_file TEXT NOT NULL DEFAULT '',
	ca_file    TEXT NOT NULL DEFAULT '',
	cert_file  TEXT NOT NULL DEFAULT '',
	key_file   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE secrets (
	tenant  TEXT NOT NULL,
	name    TEXT NOT NULL,
	sealed  BLOB NOT NULL,
	updated INTEGER NOT NULL,
	PRIMARY KEY (tenant, name)
);

CREATE TABLE jobs (
	id         TEXT PRIMARY KEY,
	tenant     TEXT NOT NULL,
	user_name  TEXT NOT NULL,
	spec       TEXT NOT NULL,
	env_names  TEXT NOT NULL DEFAULT '[]',
	env_sealed BLOB,
	state      TEXT NOT NULL,
	error      TEXT NOT NULL DEFAULT '',
	cancelled  INTEGER NOT NULL DEFAULT 0,
	created    INTEGER NOT NULL,
	finished   INTEGER
);
CREATE INDEX jobs_owner ON jobs(tenant, user_name);

CREATE TABLE job_runs (
	tenant TEXT NOT NULL,
	job    TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
	n      INTEGER NOT NULL,
	run    TEXT NOT NULL,
	PRIMARY KEY (job, n)
);

CREATE TABLE services (
	tenant    TEXT NOT NULL,
	name      TEXT NOT NULL,
	user_name TEXT NOT NULL,
	record    TEXT NOT NULL,
	PRIMARY KEY (tenant, name)
);

CREATE TABLE service_retiring (
	sandbox TEXT PRIMARY KEY
);

CREATE TABLE policies (
	name              TEXT PRIMARY KEY,
	max_lifetime_secs INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE user_policies (
	user_name TEXT NOT NULL,
	tenant    TEXT NOT NULL,
	policy    TEXT NOT NULL REFERENCES policies(name),
	PRIMARY KEY (user_name, tenant)
);
