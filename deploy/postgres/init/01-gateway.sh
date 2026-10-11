#!/bin/sh
# Run once, when the database is first created: the gateway's own login
# role, which owns its database and nothing else, and the role its requests
# take for row-level security (internal/gateway/migrations/postgres/0002).
# The gateway's schema itself is its migrations', applied as it starts.
set -eu
pw=$(cat "$GATEWAY_PASSWORD_FILE")
psql -v ON_ERROR_STOP=1 -v pw="$pw" --username "$POSTGRES_USER" --dbname postgres <<'SQL'
CREATE ROLE sandbox_gateway LOGIN PASSWORD :'pw';
CREATE ROLE sandbox_gateway_tenant NOLOGIN NOBYPASSRLS;
GRANT sandbox_gateway_tenant TO sandbox_gateway WITH ADMIN OPTION;
CREATE DATABASE sandbox_gateway OWNER sandbox_gateway;
REVOKE ALL ON DATABASE sandbox_gateway FROM PUBLIC;
SQL
