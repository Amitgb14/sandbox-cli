-- Registry logins (registry_logins.go): a user name and a sealed token per
-- organisation and registry, which the gateway adds to that organisation's
-- creates for a private image. Like secrets, one organisation's rows only,
-- under row-level security.

CREATE TABLE registry_logins (
	org_id   TEXT NOT NULL REFERENCES orgs(id),
	registry TEXT NOT NULL,
	username TEXT NOT NULL,
	sealed   BYTEA NOT NULL,
	updated  TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (org_id, registry)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON registry_logins TO sandbox_gateway_tenant;

ALTER TABLE registry_logins ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON registry_logins TO sandbox_gateway_tenant
	USING (org_id = current_setting('app.org', true)) WITH CHECK (org_id = current_setting('app.org', true));
