-- Allowlist groups (egress_groups.go): named sets of hosts an admin keeps
-- and any user names in a sandbox's network policy.

CREATE TABLE egress_groups (
	name        TEXT PRIMARY KEY,
	description TEXT NOT NULL DEFAULT '',
	hosts       TEXT NOT NULL
);
