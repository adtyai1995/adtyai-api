#!/usr/bin/env bash
# 读取 /etc/adtyai/edge-ips.txt，校验后生成源站 Nginx 的边缘节点真实 IP 与清单接口放行配置，再刷新 ufw 规则。
# 文件为空或不存在时生成空的 Nginx include 文件并清掉所有带 adtyai-edge 备注的 ufw 规则。
set -euo pipefail

EDGE_IP_LIST="${1:-${EDGE_IP_LIST:-/etc/adtyai/edge-ips.txt}}"
NGINX_REALIP_INCLUDE="${NGINX_REALIP_INCLUDE:-/etc/nginx/adty-edge-ips.conf}"
NGINX_ALLOW_INCLUDE="${NGINX_ALLOW_INCLUDE:-/etc/nginx/adty-edge-allow.conf}"
NGINX_BIN="${NGINX_BIN:-nginx}"
UFW_SCRIPT="${UFW_SCRIPT:-$(dirname "$(readlink -f "$0")")/ufw-cloudflare.sh}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
    echo "update-edge-ips: $*，保留旧文件" >&2
    exit 1
}

: >"$work/list"
if [ -f "$EDGE_IP_LIST" ]; then
    tr -d '\r' <"$EDGE_IP_LIST" | awk '{ gsub(/#.*/, ""); gsub(/^[ \t]+|[ \t]+$/, "") } $0 != "" && !seen[$0]++' >"$work/list"
fi

# 校验每一行都是合法 IPv4/IPv6 地址或 CIDR（拒绝 0.0.0.0/0 与过宽掩码）。
bad="$(python3 - "$work/list" <<'PY'
import ipaddress, sys
with open(sys.argv[1], "r", encoding="utf-8") as fh:
    for raw in fh:
        line = raw.strip()
        if not line:
            continue
        try:
            net = ipaddress.ip_network(line, strict=False)
        except ValueError:
            print(line)
            sys.exit(0)
        if net.is_private or net.is_loopback or net.is_link_local or net.is_multicast or net.is_unspecified:
            print(line)
            sys.exit(0)
        if (net.version == 4 and net.prefixlen < 24) or (net.version == 6 and net.prefixlen < 48):
            print(line)
            sys.exit(0)
PY
)"
[ -z "$bad" ] || fail "边缘节点列表包含非法或非公网地址：$bad"

{
    echo "# 由 update-edge-ips.sh 生成，不要手工改。来源：$EDGE_IP_LIST"
    awk '{ print "set_real_ip_from " $0 ";" }' "$work/list"
} >"$work/realip.conf"

{
    echo "# 由 update-edge-ips.sh 生成，不要手工改。来源：$EDGE_IP_LIST"
    awk '{ print "allow " $0 ";" }' "$work/list"
} >"$work/allow.conf"

replace_file() {
    local src="$1" dest="$2"
    mkdir -p "$(dirname "$dest")"
    cp "$src" "$dest.new"
    chmod 644 "$dest.new"
    mv -f "$dest.new" "$dest"
}

changed=0
if ! cmp -s "$work/realip.conf" "$NGINX_REALIP_INCLUDE" 2>/dev/null || ! cmp -s "$work/allow.conf" "$NGINX_ALLOW_INCLUDE" 2>/dev/null; then
    changed=1
fi

if [ "$changed" = 1 ]; then
    [ -f "$NGINX_REALIP_INCLUDE" ] && cp -p "$NGINX_REALIP_INCLUDE" "$work/realip.old"
    [ -f "$NGINX_ALLOW_INCLUDE" ] && cp -p "$NGINX_ALLOW_INCLUDE" "$work/allow.old"
    replace_file "$work/realip.conf" "$NGINX_REALIP_INCLUDE"
    replace_file "$work/allow.conf" "$NGINX_ALLOW_INCLUDE"
    if ! "$NGINX_BIN" -t -q; then
        if [ -f "$work/realip.old" ]; then mv -f "$work/realip.old" "$NGINX_REALIP_INCLUDE"; else rm -f "$NGINX_REALIP_INCLUDE"; fi
        if [ -f "$work/allow.old" ]; then mv -f "$work/allow.old" "$NGINX_ALLOW_INCLUDE"; else rm -f "$NGINX_ALLOW_INCLUDE"; fi
        fail "nginx -t 校验未通过"
    fi
    systemctl reload nginx 2>/dev/null || "$NGINX_BIN" -s reload
    echo "update-edge-ips: 已更新 Nginx 边缘节点白名单（共 $(wc -l <"$work/list") 个）"
else
    echo "update-edge-ips: 边缘节点列表无变化（共 $(wc -l <"$work/list") 个）"
fi

if [ -x "$UFW_SCRIPT" ]; then
    EDGE_IP_LIST="$EDGE_IP_LIST" "$UFW_SCRIPT"
fi
