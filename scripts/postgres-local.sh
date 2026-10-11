#!/bin/sh
# PostgreSQL for sandbox-gateway on this machine, in Docker (or Podman).
#
#   scripts/postgres-local.sh up       start it; write the gateway's DSN file
#   scripts/postgres-local.sh down     stop it; the data stays
#   scripts/postgres-local.sh dsn      print the DSN file's path
#   scripts/postgres-local.sh psql     a psql shell as the gateway's role
#   scripts/postgres-local.sh test-dsn print an admin DSN, for the tests
#                                       (each makes and drops a database)
#   scripts/postgres-local.sh destroy  stop it and delete its data
#
# SANDBOX_DB_PORT picks the loopback port (5432). The passwords are made on
# first use in deploy/postgres/secrets (mode 0600, never committed), and the
# DSN goes to ~/.config/sandbox/gateway-db.dsn (0600), for
#   sandbox-gateway --store postgres --store-dsn-file ~/.config/sandbox/gateway-db.dsn …
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
dir="$here/deploy/postgres"
port="${SANDBOX_DB_PORT:-5432}"
dsn_file="${XDG_CONFIG_HOME:-$HOME/.config}/sandbox/gateway-db.dsn"

if command -v docker >/dev/null 2>&1; then
	compose="docker compose"
elif command -v podman >/dev/null 2>&1; then
	compose="podman compose"
else
	echo "postgres-local: needs docker or podman" >&2
	exit 1
fi
c() { (cd "$dir" && SANDBOX_DB_PORT="$port" $compose "$@"); }

secret() {
	f="$dir/secrets/$1"
	if [ ! -s "$f" ]; then
		umask 077
		mkdir -p "$dir/secrets"
		head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' >"$f"
	fi
	chmod 600 "$f"
	cat "$f"
}

case "${1:-}" in
up)
	gw=$(secret gateway_password)
	secret postgres_password >/dev/null
	c up -d --wait
	umask 077
	mkdir -p "$(dirname "$dsn_file")"
	# Loopback: the gateway accepts sslmode=disable only for a database on
	# this machine.
	printf 'postgres://sandbox_gateway:%s@127.0.0.1:%s/sandbox_gateway?sslmode=disable\n' "$gw" "$port" >"$dsn_file"
	chmod 600 "$dsn_file"
	echo "PostgreSQL is up on 127.0.0.1:$port; the gateway's DSN is in $dsn_file"
	;;
down) c down ;;
destroy) c down -v ;;
dsn) echo "$dsn_file" ;;
psql) c exec postgres psql -U sandbox_gateway sandbox_gateway ;;
test-dsn) printf 'postgres://postgres:%s@127.0.0.1:%s/postgres?sslmode=disable\n' "$(secret postgres_password)" "$port" ;;
*)
	sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
