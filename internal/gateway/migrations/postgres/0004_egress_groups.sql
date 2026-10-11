-- Allowlist groups (egress_groups.go): named sets of hosts an admin keeps
-- and any user names in a sandbox's network policy. They belong to no one
-- tenant, as policies do, so a request made under row-level security reads
-- them as it reads policies.

CREATE TABLE egress_groups (
	name        TEXT PRIMARY KEY,
	description TEXT NOT NULL DEFAULT '',
	hosts       JSONB NOT NULL
);

GRANT SELECT, INSERT, UPDATE, DELETE ON egress_groups TO sandbox_gateway_tenant;
