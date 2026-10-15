-- Agent logins (agent_logins.go): a user's own login to an agent, sealed,
-- one row per user and agent, and the sandboxes it is kept from — where the
-- gateway copies it in and saves it back as it changes. A login is a
-- person's, not their organisation's: rows are keyed by user and read only
-- by the gateway itself for the user whose key asks.

CREATE TABLE agent_logins (
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	agent   TEXT NOT NULL,
	sealed  BLOB NOT NULL,
	updated INTEGER NOT NULL,
	PRIMARY KEY (user_id, agent)
);

CREATE TABLE agent_login_keeps (
	sandbox_id TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	agent      TEXT NOT NULL,
	seen       TEXT NOT NULL,
	created    INTEGER NOT NULL
);
