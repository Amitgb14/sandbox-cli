-- Organisations and users as tables of their own, and every record tied to
-- them by foreign key (sql_migrate3.go does the rest: it moves the old
-- tables aside, copies their rows across resolving each tenant to an
-- organisation and each user name to a user, drops them, and indexes these).
--
-- Every tenant is an organisation, keyed by the tenant's name: the default
-- tenant the one of kind default (keyed ''), a person's own tenant a personal
-- one, and those made through the API team ones. A user is a name in a home organisation (their keys'
-- tenant), and is a member of team organisations besides.

CREATE TABLE orgs_v3 (
	id           TEXT PRIMARY KEY,
	key          TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL,
	kind         TEXT NOT NULL CHECK (kind IN ('default', 'personal', 'team')),
	created      INTEGER NOT NULL,
	created_by   TEXT REFERENCES users_v3(id)
);

CREATE TABLE users_v3 (
	id         TEXT PRIMARY KEY,
	org_id     TEXT NOT NULL REFERENCES orgs_v3(id),
	name       TEXT NOT NULL,
	email      TEXT,
	password   TEXT,
	created    INTEGER NOT NULL,
	last_login INTEGER,
	UNIQUE (org_id, name)
);

CREATE TABLE api_keys_v3 (
	id      TEXT PRIMARY KEY,
	hash    TEXT NOT NULL UNIQUE,
	user_id TEXT NOT NULL REFERENCES users_v3(id) ON DELETE CASCADE,
	scopes  TEXT NOT NULL,
	created INTEGER NOT NULL,
	revoked INTEGER NOT NULL DEFAULT 0,
	expires INTEGER,
	account INTEGER NOT NULL DEFAULT 0,
	parent  TEXT REFERENCES api_keys_v3(id) ON DELETE CASCADE
);

CREATE TABLE invites_v3 (
	id              TEXT PRIMARY KEY,
	hash            TEXT NOT NULL UNIQUE,
	email           TEXT NOT NULL,
	org_id          TEXT NOT NULL REFERENCES orgs_v3(id),
	user_name       TEXT NOT NULL,
	scopes          TEXT NOT NULL,
	policy          TEXT NOT NULL DEFAULT '',
	expires_in_secs INTEGER NOT NULL DEFAULT 0,
	account_key     TEXT REFERENCES api_keys_v3(id) ON DELETE CASCADE,
	created         INTEGER NOT NULL,
	expires         INTEGER NOT NULL
);

CREATE TABLE ssh_keys_v3 (
	id             TEXT PRIMARY KEY,
	user_id        TEXT NOT NULL REFERENCES users_v3(id) ON DELETE CASCADE,
	fingerprint    TEXT NOT NULL,
	authorized_key TEXT NOT NULL,
	sandbox        TEXT NOT NULL DEFAULT '',
	created        INTEGER NOT NULL
);

CREATE TABLE ssh_tokens_v3 (
	hash    TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users_v3(id) ON DELETE CASCADE,
	org_id  TEXT NOT NULL REFERENCES orgs_v3(id),
	sandbox TEXT NOT NULL,
	expires INTEGER NOT NULL
);

CREATE TABLE org_members_v3 (
	org_id  TEXT NOT NULL REFERENCES orgs_v3(id) ON DELETE CASCADE,
	user_id TEXT NOT NULL REFERENCES users_v3(id) ON DELETE CASCADE,
	role    TEXT NOT NULL,
	added   INTEGER NOT NULL,
	PRIMARY KEY (org_id, user_id)
);

CREATE TABLE sandboxes_v3 (
	id                TEXT PRIMARY KEY,
	org_id            TEXT NOT NULL REFERENCES orgs_v3(id),
	user_id           TEXT NOT NULL REFERENCES users_v3(id),
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

CREATE TABLE volumes_v3 (
	name    TEXT PRIMARY KEY,
	org_id  TEXT NOT NULL REFERENCES orgs_v3(id),
	user_id TEXT NOT NULL REFERENCES users_v3(id),
	node    TEXT NOT NULL
);

CREATE TABLE snapshots_v3 (
	id      TEXT PRIMARY KEY,
	org_id  TEXT NOT NULL REFERENCES orgs_v3(id),
	user_id TEXT NOT NULL REFERENCES users_v3(id),
	node    TEXT NOT NULL
);

CREATE TABLE secrets_v3 (
	org_id  TEXT NOT NULL REFERENCES orgs_v3(id),
	name    TEXT NOT NULL,
	sealed  BLOB NOT NULL,
	updated INTEGER NOT NULL,
	PRIMARY KEY (org_id, name)
);

CREATE TABLE jobs_v3 (
	id         TEXT PRIMARY KEY,
	org_id     TEXT NOT NULL REFERENCES orgs_v3(id),
	user_id    TEXT NOT NULL REFERENCES users_v3(id),
	spec       TEXT NOT NULL,
	env_names  TEXT NOT NULL DEFAULT '[]',
	env_sealed BLOB,
	state      TEXT NOT NULL,
	error      TEXT NOT NULL DEFAULT '',
	cancelled  INTEGER NOT NULL DEFAULT 0,
	created    INTEGER NOT NULL,
	finished   INTEGER
);

CREATE TABLE job_runs_v3 (
	org_id TEXT NOT NULL REFERENCES orgs_v3(id),
	job    TEXT NOT NULL REFERENCES jobs_v3(id) ON DELETE CASCADE,
	n      INTEGER NOT NULL,
	run    TEXT NOT NULL,
	PRIMARY KEY (job, n)
);

CREATE TABLE services_v3 (
	org_id  TEXT NOT NULL REFERENCES orgs_v3(id),
	name    TEXT NOT NULL,
	user_id TEXT NOT NULL REFERENCES users_v3(id),
	record  TEXT NOT NULL,
	PRIMARY KEY (org_id, name)
);

CREATE TABLE user_policies_v3 (
	user_id TEXT PRIMARY KEY REFERENCES users_v3(id) ON DELETE CASCADE,
	policy  TEXT NOT NULL REFERENCES policies(name)
);
