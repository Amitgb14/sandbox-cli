#!/bin/sh
# Make the certificates a gateway and its nodes need (docs/fleet.md):
#
#   - a private CA, which signs everything below and which every node trusts
#     for client certificates (sandboxd --client-ca) and the gateway trusts for
#     node certificates (ca_file);
#   - the gateway's client certificate, which it presents to every node
#     (cert_file, key_file);
#   - one server certificate per node, for the name or address the gateway
#     dials (sandboxd --tls-cert, --tls-key);
#   - with -g, a server certificate for the gateway's own API (sandbox-gateway
#     serve --tls-cert, --tls-key). A certificate from a CA your users already
#     trust is better there; this one is for a gateway reached on a private
#     network, whose users are given ca.pem (sandbox-cli context add --ca).
#
#   sh packaging/fleet/make-certs.sh [-o DIR] [-d DAYS] [-g GATEWAY_HOST] NODE_HOST...
#
#   sh packaging/fleet/make-certs.sh -o fleet-certs -g gateway.example.internal 10.0.0.17 10.0.0.18
#
# A NODE_HOST is the host part of the node's endpoint exactly as the gateway
# will dial it: an IP address becomes an IP SAN, anything else a DNS SAN. The
# files for a node are named after it: node-10.0.0.17.pem, node-10.0.0.17-key.pem.
#
# Run it again with the same -o to add nodes: an existing CA is reused, and an
# existing certificate is never overwritten (remove it first to reissue).
#
# Every private key is written 0600 and the directory is made 0700. ca-key.pem
# can mint a certificate every node accepts: keep it off the nodes and off the
# gateway, ideally offline, once the certificates are made.
#
# POSIX sh; needs openssl 1.1.1 or later.

set -eu

OUT=fleet-certs
DAYS=825
GATEWAY_HOST=""

usage() { sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//'; }
die() { printf 'make-certs: %s\n' "$*" >&2; exit 1; }

while getopts o:d:g:h opt; do
  case "$opt" in
    o) OUT=$OPTARG ;;
    d) DAYS=$OPTARG ;;
    g) GATEWAY_HOST=$OPTARG ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

[ $# -gt 0 ] || [ -n "$GATEWAY_HOST" ] || [ ! -f "$OUT/ca.pem" ] || die "nothing to make: name a node host or -g GATEWAY_HOST"
command -v openssl >/dev/null 2>&1 || die "openssl is not on PATH"
case "$DAYS" in ''|*[!0-9]*) die "-d wants a number of days" ;; esac

# A host goes into a file name and a SAN; refuse anything that is neither a
# host name nor an address, so it cannot be read as openssl config syntax or
# a path.
check_host() {
  case "$1" in
    ''|-*|*[!A-Za-z0-9.:-]*) die "\"$1\" is not a host name or an IP address" ;;
  esac
}

# The SAN for a host: an address (IPv4, or IPv6 with a colon) or a name.
san_for() {
  case "$1" in
    *:*) printf 'IP:%s' "$1" ;;
    *[!0-9.]*) printf 'DNS:%s' "$1" ;;
    *) printf 'IP:%s' "$1" ;;
  esac
}

# Every name is checked before anything is written.
[ -z "$GATEWAY_HOST" ] || check_host "$GATEWAY_HOST"
for host in "$@"; do check_host "$host"; done

umask 077
mkdir -p "$OUT"
chmod 700 "$OUT"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

newkey() { # newkey FILE — a P-256 key, 0600 by the umask above
  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$1" 2>/dev/null
}

if [ -f "$OUT/ca.pem" ] && [ -f "$OUT/ca-key.pem" ]; then
  printf 'reusing the CA in %s\n' "$OUT"
elif [ -f "$OUT/ca.pem" ] || [ -f "$OUT/ca-key.pem" ]; then
  die "$OUT has half a CA (ca.pem or ca-key.pem without the other); move it aside"
else
  newkey "$OUT/ca-key.pem"
  openssl req -x509 -new -key "$OUT/ca-key.pem" -sha256 -days 3650 \
    -subj "/CN=sandbox fleet CA" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -out "$OUT/ca.pem"
  chmod 644 "$OUT/ca.pem"
  printf 'made %s/ca.pem (keep ca-key.pem offline)\n' "$OUT"
fi

# leaf NAME CN USAGE [SAN] — a certificate signed by the CA.
leaf() {
  name=$1 cn=$2 usage=$3 san=${4:-}
  if [ -e "$OUT/$name.pem" ] || [ -e "$OUT/$name-key.pem" ]; then
    printf 'kept %s/%s.pem (exists; remove it and its key to reissue)\n' "$OUT" "$name"
    return 0
  fi
  {
    echo "basicConstraints=critical,CA:FALSE"
    echo "keyUsage=critical,digitalSignature"
    echo "extendedKeyUsage=$usage"
    [ -z "$san" ] || echo "subjectAltName=$san"
  } > "$TMP/ext"
  newkey "$OUT/$name-key.pem"
  openssl req -new -key "$OUT/$name-key.pem" -subj "/CN=$cn" -out "$TMP/csr"
  openssl x509 -req -in "$TMP/csr" -CA "$OUT/ca.pem" -CAkey "$OUT/ca-key.pem" \
    -set_serial "0x$(openssl rand -hex 16)" -sha256 -days "$DAYS" \
    -extfile "$TMP/ext" -out "$OUT/$name.pem" 2>/dev/null
  chmod 644 "$OUT/$name.pem"
  printf 'made %s/%s.pem\n' "$OUT" "$name"
}

# The gateway's client certificate: what every node's --client-ca checks.
leaf gateway-client "sandbox-gateway" clientAuth

if [ -n "$GATEWAY_HOST" ]; then
  leaf gateway "$GATEWAY_HOST" serverAuth "$(san_for "$GATEWAY_HOST")"
fi

for host in "$@"; do
  leaf "node-$host" "$host" serverAuth "$(san_for "$host")"
done
