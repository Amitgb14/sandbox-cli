-- Row-level security: the second wall between tenants (sql_store.go,
-- ForTenant). A request's statements run as sandbox_gateway_tenant with
-- app.tenant set to the tenant it acts in, and these tables show and take
-- only that tenant's rows, whatever a query's own filter says.
--
-- The gateway's login role owns the tables, and an owner is not held to
-- row-level security unless it is forced, which it is not: the gateway's own
-- work across tenants (the scheduler, reconcile, the job runner, admin
-- endpoints) runs as the owner. So no role needs BYPASSRLS, which a managed
-- database rarely lets anyone grant.

DO $$
BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'sandbox_gateway_tenant') THEN
		CREATE ROLE sandbox_gateway_tenant NOLOGIN NOBYPASSRLS;
	END IF;
END
$$;
GRANT sandbox_gateway_tenant TO CURRENT_USER;
GRANT USAGE ON SCHEMA public TO sandbox_gateway_tenant;

-- What a request needs that belongs to no one tenant: who the caller is,
-- organisations, policies, nodes. Read and written as today, by the
-- gateway's own checks.
GRANT SELECT, INSERT, UPDATE, DELETE ON
	api_keys, invites, ssh_keys, ssh_tokens, orgs, org_members, nodes, policies, user_policies, service_retiring
	TO sandbox_gateway_tenant;

-- A tenant's own records.
GRANT SELECT, INSERT, UPDATE, DELETE ON
	sandboxes, volumes, snapshots, secrets, jobs, job_runs, services
	TO sandbox_gateway_tenant;

ALTER TABLE sandboxes ENABLE ROW LEVEL SECURITY;
ALTER TABLE volumes   ENABLE ROW LEVEL SECURITY;
ALTER TABLE snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE secrets   ENABLE ROW LEVEL SECURITY;
ALTER TABLE jobs      ENABLE ROW LEVEL SECURITY;
ALTER TABLE job_runs  ENABLE ROW LEVEL SECURITY;
ALTER TABLE services  ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON sandboxes TO sandbox_gateway_tenant
	USING (tenant = current_setting('app.tenant', true)) WITH CHECK (tenant = current_setting('app.tenant', true));
CREATE POLICY tenant_isolation ON volumes TO sandbox_gateway_tenant
	USING (tenant = current_setting('app.tenant', true)) WITH CHECK (tenant = current_setting('app.tenant', true));
CREATE POLICY tenant_isolation ON snapshots TO sandbox_gateway_tenant
	USING (tenant = current_setting('app.tenant', true)) WITH CHECK (tenant = current_setting('app.tenant', true));
CREATE POLICY tenant_isolation ON secrets TO sandbox_gateway_tenant
	USING (tenant = current_setting('app.tenant', true)) WITH CHECK (tenant = current_setting('app.tenant', true));
CREATE POLICY tenant_isolation ON jobs TO sandbox_gateway_tenant
	USING (tenant = current_setting('app.tenant', true)) WITH CHECK (tenant = current_setting('app.tenant', true));
CREATE POLICY tenant_isolation ON job_runs TO sandbox_gateway_tenant
	USING (tenant = current_setting('app.tenant', true)) WITH CHECK (tenant = current_setting('app.tenant', true));
CREATE POLICY tenant_isolation ON services TO sandbox_gateway_tenant
	USING (tenant = current_setting('app.tenant', true)) WITH CHECK (tenant = current_setting('app.tenant', true));
