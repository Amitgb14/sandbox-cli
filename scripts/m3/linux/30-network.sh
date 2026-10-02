#!/usr/bin/env bash
# Egress enforced on the host: a tap device, nftables redirecting the guest's
# tcp/80 and tcp/443 to the allowlist proxy and dropping everything else, and
# the proxy deciding by name (SNI, Host header, or CONNECT). Needs root.
#
# The guest runs eleven probes and prints, for each, what was expected and what
# happened. Allowlist: github.com and example.com. Every UNEXPECTED line is a
# finding. Nothing is left behind: tap, nftables table and proxy are removed on
# exit, whatever happens.
source "$(dirname "$0")/lib.sh"
need_root "the network test"; need_kvm; need_kernel; fetch_firecracker; build_rootfs; header

tap=tap-m3
table=m3sbx
PROXY_PID=
cleanup() {
  stop_vm
  [ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null || true
  nft delete table inet "$table" 2>/dev/null || true
  ip link del "$tap" 2>/dev/null || true
}
trap cleanup EXIT
cleanup

# Another firewall on the host evaluates the same packets, and in nftables a
# reject in any table wins over an accept in ours. Record which one is active so
# a refused probe can be read for what it is.
fw=none
systemctl is-active --quiet firewalld 2>/dev/null && fw="firewalld (zone $(firewall-cmd --get-default-zone 2>/dev/null))"
command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active" && fw="ufw"
record "net: host firewall also active" "$fw"

say "tap, nftables, proxy"
ip tuntap add dev "$tap" mode tap
ip addr add 172.16.0.1/30 dev "$tap"
ip link set "$tap" up
nft -f - <<NFT
table inet $table {
  chain prerouting {
    type nat hook prerouting priority dstnat;
    iifname "$tap" tcp dport { 80, 443 } redirect to :3128
  }
  chain input {
    type filter hook input priority filter;
    iifname "$tap" ct state established,related accept
    iifname "$tap" tcp dport 3128 accept
    iifname "$tap" counter drop
  }
  chain forward {
    type filter hook forward priority filter;
    iifname "$tap" counter drop
  }
}
NFT
"$M3_WORK/bin/m3tool" proxy --listen 172.16.0.1:3128 --allow github.com,example.com 2> "$M3_WORK/proxy.log" &
PROXY_PID=$!
sleep 0.5

write_config "$M3_WORK/net.json" net 512 net
log="$M3_WORK/net.log"
start_vm "$M3_WORK/net.json" "$log"
wait_for "$log" "M3 DONE" 180
grep '^M3 NET' "$log" | while read -r _ _ name rest; do record "net: $name" "$rest"; done
record "net: unexpected results" "$(grep -c 'UNEXPECTED' "$log" || true)"
record "net: packets dropped from the guest (input, forward)" "$(nft list table inet "$table" | awk '/counter packets/{for(i=1;i<=NF;i++) if($i=="packets") print $(i+1)}' | xargs)"
cp "$M3_WORK/proxy.log" "$M3_WORK/proxy-decisions.log"
say "proxy decisions: $M3_WORK/proxy-decisions.log"
