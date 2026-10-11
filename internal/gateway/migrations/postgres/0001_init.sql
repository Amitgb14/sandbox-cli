-- The gateway's state, on PostgreSQL (sql_store.go). Times are
-- timestamptz; JSON is jsonb. Every tenant-scoped table has `tenant`, '' for
-- the default tenant.

CREATE TABLE api_keys (
	id         TEXT PRIMARY KEY,
	hash       TEXT NOT NULL UNIQUE,
	user_name  TEXT NOT NULL,
	tenant     TEXT NOT NULL,
	scopes     JSONB NOT NULL,
	created    TIMESTAMPTZ NOT NULL,
	revoked    BOOLEAN NOT NULL DEFAULT FALSE,
	expires    TIMESTAMPTZ,
	email      TEXT,
	password   TEXT,
	last_login TIMESTAMPTZ,
	parent     TEXT REFERENCES api_keys(id) ON DELETE CASCADE
);
CREATE INDEX api_keys_user ON api_keys(user_name, tenant);
CREATE INDEX api_keys_parent ON api_keys(parent);
-- One live account per address, and per user.
CREATE UNIQUE INDEX api_keys_account_email ON api_keys(email) WHERE email IS NOT NULL AND NOT revoked AND parent IS NULL;
CREATE UNIQUE INDEX api_keys_account_user ON api_keys(user_name, tenant) WHERE email IS NOT NULL AND NOT revoked AND parent IS NULL;

CREATE TABLE invites (
	id              TEXT PRIMARY KEY,
	hash            TEXT NOT NULL UNIQUE,
	email           TEXT NOT NULL,
	user_name       TEXT NOT NULL,
	tenant          TEXT NOT NULL,
	scopes          JSONB NOT NULL,
	policy          TEXT NOT NULL DEFAULT '',
	expires_in_secs BIGINT NOT NULL DEFAULT 0,
	account_key     TEXT,
	created         TIMESTAMPTZ NOT NULL,
	expires         TIMESTAMPTZ NOT NULL
);
CREATE INDEX invites_email ON invites(email);

CREATE TABLE ssh_keys (
	id             TEXT PRIMARY KEY,
	user_name      TEXT NOT NULL,
	tenant         TEXT NOT NULL,
	fingerprint    TEXT NOT NULL,
	authorized_key TEXT NOT NULL,
	sandbox        TEXT NOT NULL DEFAULT '',
	created        TIMESTAMPTZ NOT NULL
);
CREATE INDEX ssh_keys_fingerprint ON ssh_keys(fingerprint);
CREATE INDEX ssh_keys_user ON ssh_keys(user_name);

CREATE TABLE ssh_tokens (
	hash      TEXT PRIMARY KEY,
	user_name TEXT NOT NULL,
	tenant    TEXT NOT NULL,
	sandbox   TEXT NOT NULL,
	expires   TIMESTAMPTZ NOT NULL
);
CREATE INDEX ssh_tokens_expires ON ssh_tokens(expires);

CREATE TABLE orgs (
	name_key          TEXT PRIMARY KEY,
	display_name      TEXT NOT NULL,
	created           TIMESTAMPTZ NOT NULL,
	created_by        TEXT NOT NULL,
	created_by_tenant TEXT NOT NULL DEFAULT ''
);

CREATE TABLE org_members (
	org         TEXT NOT NULL REFERENCES orgs(name_key) ON DELETE CASCADE,
	user_name   TEXT NOT NULL,
	user_tenant TEXT NOT NULL,
	role        TEXT NOT NULL,
	added       TIMESTAMPTZ NOT NULL,
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
	cpus              DOUBLE PRECISION NOT NULL DEFAULT 0,
	memory_mb         BIGINT NOT NULL DEFAULT 0,
	disk_mb           BIGINT NOT NULL DEFAULT 0,
	recorded          TIMESTAMPTZ NOT NULL,
	name              TEXT NOT NULL DEFAULT '',
	image             TEXT NOT NULL DEFAULT '',
	state             TEXT NOT NULL DEFAULT '',
	labels            JSONB NOT NULL DEFAULT '{}'::jsonb,
	network           JSONB NOT NULL DEFAULT '{}'::jsonb,
	lifetime_secs     BIGINT NOT NULL DEFAULT 0,
	expires_at        TIMESTAMPTZ,
	idle_timeout_secs BIGINT NOT NULL DEFAULT 0,
	created_at        TIMESTAMPTZ,
	details_updated   TIMESTAMPTZ,
	terminated_at     TIMESTAMPTZ,
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
	sealed  BYTEA NOT NULL,
	updated TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (tenant, name)
);

CREATE TABLE jobs (
	id         TEXT PRIMARY KEY,
	tenant     TEXT NOT NULL,
	user_name  TEXT NOT NULL,
	spec       JSONB NOT NULL,
	env_names  JSONB NOT NULL DEFAULT '[]'::jsonb,
	env_sealed BYTEA,
	state      TEXT NOT NULL,
	error      TEXT NOT NULL DEFAULT '',
	cancelled  BOOLEAN NOT NULL DEFAULT FALSE,
	created    TIMESTAMPTZ NOT NULL,
	finished   TIMESTAMPTZ
);
CREATE INDEX jobs_owner ON jobs(tenant, user_name);

CREATE TABLE job_runs (
	tenant TEXT NOT NULL,
	job    TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
	n      BIGINT NOT NULL,
	run    JSONB NOT NULL,
	PRIMARY KEY (job, n)
);

CREATE TABLE services (
	tenant    TEXT NOT NULL,
	name      TEXT NOT NULL,
	user_name TEXT NOT NULL,
	record    JSONB NOT NULL,
	PRIMARY KEY (tenant, name)
);

CREATE TABLE service_retiring (
	sandbox TEXT PRIMARY KEY
);

CREATE TABLE policies (
	name              TEXT PRIMARY KEY,
	max_lifetime_secs BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE user_policies (
	user_name TEXT NOT NULL,
	tenant    TEXT NOT NULL,
	policy    TEXT NOT NULL REFERENCES policies(name),
	PRIMARY KEY (user_name, tenant)
);
