#!/usr/bin/env python3
"""Synchronize ADTY AI mainland edge Nginx configuration and certificates from origin.

Stdlib-only Python 3 script run periodically by adty-edge-sync.timer.
Fetches GET /api/adty/internal/edge/manifest using X-Adty-Edge-Token + ETag/304,
validates hostnames and anti-mass-deletion invariants, writes certificates (0644/0600),
generates Nginx stream/HTTP snippets, validates with `nginx -t` (rolling back on error),
and reloads Nginx only when valid changes were applied.
"""

from __future__ import annotations

import argparse
import hashlib
import http.client
import json
import os
import pathlib
import re
import secrets
import shlex
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.parse
from dataclasses import dataclass
from typing import Any

HOSTNAME_RE = re.compile(
    r"^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$"
)
IPV4_RE = re.compile(r"^(?:\d{1,3}\.){3}\d{1,3}$")


class SyncError(RuntimeError):
    """Raised when manifest fetch, validation, or Nginx reload fails."""


@dataclass
class EdgeConfig:
    manifest_url: str
    edge_token: str
    conf_dir: pathlib.Path
    origin_connect_ip: str = ""
    origin_ca_file: str = ""
    nginx_bin: str = "nginx"
    nginx_reload_cmd: str = "systemctl reload nginx"
    cert_retention_days: int = 7
    min_drop_ratio: float = 0.5
    allow_mass_drop: bool = False
    http_timeout: int = 20
    apex_redirect_host: str = ""
    apex_redirect_target: str = ""
    apex_acme_email: str = ""
    apex_acme_staging: bool = False
    apex_webroot: pathlib.Path = pathlib.Path("/var/www/adty-acme")
    apex_cert_root: pathlib.Path = pathlib.Path("/etc/letsencrypt/live")
    apex_renew_within_seconds: int = 30 * 86400
    apex_retry_backoff_seconds: int = 1800
    certbot_bin: str = "certbot"
    openssl_bin: str = "openssl"

    @classmethod
    def from_env(cls, env: dict[str, str] | None = None) -> "EdgeConfig":
        src = os.environ if env is None else env
        manifest_url = src.get("ORIGIN_MANIFEST_URL", "").strip()
        edge_token = src.get("EDGE_TOKEN", "").strip()
        if not manifest_url:
            raise SyncError("ORIGIN_MANIFEST_URL is required")
        if not edge_token:
            raise SyncError("EDGE_TOKEN is required")
        return cls(
            manifest_url=manifest_url,
            edge_token=edge_token,
            conf_dir=pathlib.Path(src.get("EDGE_CONF_DIR", "/etc/nginx/adty-edge").strip()),
            origin_connect_ip=src.get("ORIGIN_CONNECT_IP", "").strip(),
            origin_ca_file=src.get("ORIGIN_CA_FILE", "").strip(),
            nginx_bin=src.get("NGINX_BIN", "nginx").strip() or "nginx",
            nginx_reload_cmd=src.get("NGINX_RELOAD_CMD", "systemctl reload nginx").strip()
            or "systemctl reload nginx",
            cert_retention_days=max(1, int(src.get("CERT_RETENTION_DAYS", "7"))),
            min_drop_ratio=float(src.get("MIN_DROP_RATIO", "0.5")),
            allow_mass_drop=src.get("ALLOW_MASS_DROP", "").strip() in {"1", "true", "yes"},
            http_timeout=max(5, int(src.get("HTTP_TIMEOUT", "20"))),
            apex_redirect_host=src.get("APEX_REDIRECT_HOST", "").strip(),
            apex_redirect_target=src.get("APEX_REDIRECT_TARGET", "").strip(),
            apex_acme_email=src.get("APEX_ACME_EMAIL", "").strip(),
            apex_acme_staging=src.get("APEX_ACME_STAGING", "").strip() in {"1", "true", "yes"},
            apex_webroot=pathlib.Path(
                src.get("APEX_WEBROOT", "/var/www/adty-acme").strip() or "/var/www/adty-acme"
            ),
            apex_cert_root=pathlib.Path(
                src.get("APEX_CERT_ROOT", "/etc/letsencrypt/live").strip()
                or "/etc/letsencrypt/live"
            ),
            apex_renew_within_seconds=max(
                86400, int(src.get("APEX_RENEW_WITHIN_SECONDS", str(30 * 86400)))
            ),
            apex_retry_backoff_seconds=max(
                60, int(src.get("APEX_RETRY_BACKOFF_SECONDS", "1800"))
            ),
            certbot_bin=src.get("CERTBOT_BIN", "certbot").strip() or "certbot",
            openssl_bin=src.get("OPENSSL_BIN", "openssl").strip() or "openssl",
        )


def validate_hostname(raw: str) -> str:
    host = raw.strip().lower().rstrip(".")
    if not host or len(host) > 253 or not HOSTNAME_RE.match(host):
        raise SyncError(f"Invalid hostname in manifest: {raw!r}")
    return host


def validate_connect_host(raw: str) -> str:
    value = raw.strip()
    if not value:
        return ""
    if IPV4_RE.match(value):
        try:
            socket.inet_aton(value)
            return value
        except OSError as exc:
            raise SyncError(f"Invalid ORIGIN_CONNECT_IP: {value!r}") from exc
    try:
        socket.inet_pton(socket.AF_INET6, value.strip("[]"))
        return f"[{value.strip('[]')}]"
    except OSError:
        return validate_hostname(value)


def escape_nginx_regex(text: str) -> str:
    return re.escape(text)


def safe_file_slug(name: str) -> str:
    slug = re.sub(r"[^a-z0-9._-]", "_", name.lower())
    return slug.strip("._") or "cert"


def normalize_apex_redirect(cfg: EdgeConfig) -> tuple[str, str]:
    raw_host = cfg.apex_redirect_host.strip()
    if not raw_host:
        return "", ""
    host = validate_hostname(raw_host)
    raw_target = cfg.apex_redirect_target.strip()
    if not raw_target:
        if host.startswith("www."):
            raise SyncError(
                "APEX_REDIRECT_TARGET is required when APEX_REDIRECT_HOST starts with www."
            )
        return host, f"https://www.{host}"
    parsed = urllib.parse.urlparse(raw_target)
    if parsed.scheme != "https" or not parsed.hostname:
        raise SyncError(f"APEX_REDIRECT_TARGET must be an https://<hostname> URL: {raw_target!r}")
    if parsed.path not in {"", "/"} or parsed.query or parsed.fragment or parsed.username or parsed.password:
        raise SyncError(
            f"APEX_REDIRECT_TARGET must only specify scheme and host (no path/query): {raw_target!r}"
        )
    target_host = validate_hostname(parsed.hostname)
    if target_host == host:
        raise SyncError(f"APEX_REDIRECT_TARGET host cannot equal APEX_REDIRECT_HOST ({host})")
    port_suffix = f":{parsed.port}" if parsed.port else ""
    return host, f"https://{target_host}{port_suffix}"


def apex_cert_paths(cfg: EdgeConfig, host: str) -> tuple[pathlib.Path, pathlib.Path]:
    live_dir = cfg.apex_cert_root / host
    return live_dir / "fullchain.pem", live_dir / "privkey.pem"


def apex_cert_present(cfg: EdgeConfig, host: str) -> bool:
    fullchain, privkey = apex_cert_paths(cfg, host)
    try:
        if not fullchain.is_file() or not privkey.is_file():
            return False
        cert_text = fullchain.read_text(encoding="utf-8", errors="ignore")
        key_text = privkey.read_text(encoding="utf-8", errors="ignore")
        return "BEGIN CERTIFICATE" in cert_text and "PRIVATE KEY" in key_text
    except OSError:
        return False


def compute_apex_fingerprint(cfg: EdgeConfig) -> str:
    host, target = normalize_apex_redirect(cfg)
    if not host:
        return ""
    h = hashlib.sha256()
    h.update(f"{host}\0{target}\0".encode("utf-8"))
    if apex_cert_present(cfg, host):
        fullchain, privkey = apex_cert_paths(cfg, host)
        h.update(b"present\0")
        h.update(fullchain.read_bytes())
        h.update(b"\0")
        h.update(privkey.read_bytes())
    else:
        h.update(b"missing")
    return h.hexdigest()


def apex_cert_needs_renewal(cfg: EdgeConfig, host: str) -> bool:
    if not apex_cert_present(cfg, host):
        return True
    fullchain, _ = apex_cert_paths(cfg, host)
    proc = subprocess.run(
        [
            cfg.openssl_bin,
            "x509",
            "-in",
            str(fullchain),
            "-noout",
            "-checkend",
            str(cfg.apex_renew_within_seconds),
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    return proc.returncode != 0


def _http_get_no_redirect(connect_ip: str, host: str, path: str, timeout: float) -> tuple[int, str, str]:
    conn = http.client.HTTPConnection(connect_ip, 80, timeout=timeout)
    try:
        conn.request("GET", path, headers={"Host": host, "User-Agent": "adty-edge-selfcheck/1.0"})
        resp = conn.getresponse()
        body = resp.read(256).decode("utf-8", errors="ignore").strip()
        server = (resp.getheader("Server") or "").strip()
        return resp.status, body, server
    finally:
        conn.close()


def probe_apex_http01_readiness(cfg: EdgeConfig, host: str) -> tuple[bool, str]:
    challenge_dir = cfg.apex_webroot / ".well-known" / "acme-challenge"
    challenge_dir.mkdir(parents=True, exist_ok=True)
    token_name = f"adty-selfcheck-{secrets.token_hex(8)}"
    token_value = secrets.token_hex(16)
    token_path = challenge_dir / token_name
    req_path = f"/.well-known/acme-challenge/{token_name}"

    token_path.write_text(token_value, encoding="utf-8", newline="\n")
    os.chmod(token_path, 0o644)
    try:
        try:
            local_status, local_body, _ = _http_get_no_redirect("127.0.0.1", host, req_path, 3.0)
        except OSError as exc:
            return False, f"local Nginx port 80 unreachable for {host}: {exc}"
        if local_status != 200 or local_body != token_value:
            return (
                False,
                f"local Nginx port 80 is not serving {host} ACME webroot (HTTP {local_status})",
            )

        try:
            infos = socket.getaddrinfo(host, 80, socket.AF_INET, socket.SOCK_STREAM)
        except OSError as exc:
            return False, f"DNS lookup failed for {host}: {exc}"
        resolved_ips = list(dict.fromkeys(str(info[4][0]) for info in infos))
        if not resolved_ips:
            return False, f"DNS has no IPv4 record for {host}"

        probe_ip = resolved_ips[0]
        try:
            pub_status, pub_body, pub_server = _http_get_no_redirect(probe_ip, host, req_path, 4.0)
        except OSError as exc:
            return (
                True,
                f"local webroot ok; public self-probe to {probe_ip} timed out/errored ({exc}), attempting certbot",
            )

        if pub_status == 200 and pub_body == token_value:
            return True, f"public HTTP-01 verified via {probe_ip}"
        server_note = f", Server={pub_server}" if pub_server else ""
        return (
            False,
            f"public HTTP-01 for {host} ({probe_ip}) returned HTTP {pub_status}{server_note}; DNS not yet routed to this node",
        )
    finally:
        try:
            token_path.unlink(missing_ok=True)
        except OSError:
            pass


def ensure_apex_cert(cfg: EdgeConfig, now: float | None = None) -> bool:
    host, _ = normalize_apex_redirect(cfg)
    if not host:
        return False
    if not apex_cert_needs_renewal(cfg, host):
        return False

    now_ts = time.time() if now is None else now
    apex_state_path = cfg.conf_dir / ".apex-cert-state.json"
    apex_state = load_state(apex_state_path)
    last_failed_at = float(apex_state.get("last_failed_at") or 0)
    if last_failed_at > 0 and (now_ts - last_failed_at) < cfg.apex_retry_backoff_seconds:
        return False

    ready, reason = probe_apex_http01_readiness(cfg, host)
    if not ready:
        print(f"adty-edge-sync: apex cert skipped for {host}: {reason}", file=sys.stderr)
        return False

    cmd = [
        cfg.certbot_bin,
        "certonly",
        "--non-interactive",
        "--agree-tos",
        "--keep-until-expiring",
        "--cert-name",
        host,
        "--webroot",
        "-w",
        str(cfg.apex_webroot),
        "-d",
        host,
    ]
    if cfg.apex_acme_email:
        cmd.extend(["--email", cfg.apex_acme_email])
    else:
        cmd.append("--register-unsafely-without-email")
    if cfg.apex_acme_staging:
        cmd.append("--staging")

    proc = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if proc.returncode != 0:
        err_msg = (proc.stderr or proc.stdout or "").strip()[-400:]
        save_state(
            apex_state_path,
            {
                "host": host,
                "last_failed_at": int(now_ts),
                "last_error": err_msg,
            },
        )
        print(f"adty-edge-sync: apex certbot failed for {host}: {err_msg}", file=sys.stderr)
        return False

    save_state(
        apex_state_path,
        {
            "host": host,
            "last_failed_at": 0,
            "last_issued_at": int(now_ts),
            "last_error": "",
        },
    )
    print(f"adty-edge-sync: apex certificate ready for {host} ({reason})")
    return True


class DirectHTTPSConnection(http.client.HTTPSConnection):
    """HTTPSConnection that dials an explicit IP while sending the URL hostname as TLS SNI."""

    def __init__(
        self,
        host: str,
        port: int,
        connect_host: str,
        timeout: int,
        context: ssl.SSLContext,
    ) -> None:
        super().__init__(host=host, port=port, timeout=timeout, context=context)
        self._connect_host = connect_host.strip("[]")

    def connect(self) -> None:
        sock = socket.create_connection((self._connect_host, self.port), self.timeout)
        self.sock = self._context.wrap_socket(sock, server_hostname=self.host)


def fetch_manifest(cfg: EdgeConfig, etag: str = "") -> tuple[int, str, dict[str, Any] | None]:
    parsed = urllib.parse.urlparse(cfg.manifest_url)
    if parsed.scheme != "https" or not parsed.hostname:
        raise SyncError("ORIGIN_MANIFEST_URL must be an https:// URL")
    port = parsed.port or 443
    path = parsed.path or "/"
    if parsed.query:
        path = f"{path}?{parsed.query}"

    ssl_ctx = ssl.create_default_context()
    if cfg.origin_ca_file:
        ssl_ctx.load_verify_locations(cafile=cfg.origin_ca_file)
    ssl_ctx.minimum_version = ssl.TLSVersion.TLSv1_2

    connect_host = validate_connect_host(cfg.origin_connect_ip) if cfg.origin_connect_ip else ""
    if connect_host:
        conn: http.client.HTTPSConnection = DirectHTTPSConnection(
            host=parsed.hostname,
            port=port,
            connect_host=connect_host,
            timeout=cfg.http_timeout,
            context=ssl_ctx,
        )
    else:
        conn = http.client.HTTPSConnection(
            host=parsed.hostname,
            port=port,
            timeout=cfg.http_timeout,
            context=ssl_ctx,
        )

    headers = {
        "Host": parsed.netloc,
        "X-Adty-Edge-Token": cfg.edge_token,
        "Accept": "application/json",
        "User-Agent": "adty-edge-sync/1.0",
    }
    if etag:
        headers["If-None-Match"] = etag

    try:
        conn.request("GET", path, headers=headers)
        resp = conn.getresponse()
        body = resp.read()
        resp_etag = (resp.getheader("ETag") or "").strip()
        if resp.status == 304:
            return 304, resp_etag or etag, None
        if resp.status != 200:
            raise SyncError(f"Manifest request failed with HTTP {resp.status}")
        try:
            payload = json.loads(body.decode("utf-8"))
        except Exception as exc:
            raise SyncError(f"Manifest response is not valid JSON: {exc}") from exc
        if not isinstance(payload, dict):
            raise SyncError("Manifest JSON must be an object")
        return 200, resp_etag, payload
    finally:
        conn.close()


def render_configs(
    manifest: dict[str, Any],
    cfg: EdgeConfig,
    certs_dir: pathlib.Path,
) -> tuple[dict[str, str], dict[str, tuple[str, str]], int]:
    """Validate manifest and return (relative_conf_files, cert_files, active_domain_count)."""
    edge_enabled = bool(manifest.get("edge_mainland_enabled", False))
    fallback_origin = validate_hostname(str(manifest.get("fallback_origin") or ""))
    cf_target = validate_hostname(str(manifest.get("cloudflare_cname_target") or fallback_origin))
    origin_upstream_host = validate_connect_host(cfg.origin_connect_ip) or fallback_origin

    platform_roots = [
        validate_hostname(str(r))
        for r in (manifest.get("platform_root_domains") or [])
        if str(r).strip()
    ]

    wildcard_by_root: dict[str, dict[str, Any]] = {}
    for wc in manifest.get("wildcard_certs") or []:
        root = validate_hostname(str(wc.get("root_domain") or ""))
        cert_pem = str(wc.get("cert_pem") or "").strip()
        key_pem = str(wc.get("key_pem") or "").strip()
        if cert_pem and key_pem:
            wildcard_by_root[root] = {"cert_pem": cert_pem + "\n", "key_pem": key_pem + "\n"}

    custom_domains: list[dict[str, Any]] = []
    seen_custom: set[str] = set()
    for item in manifest.get("custom_domains") or []:
        host = validate_hostname(str(item.get("hostname") or ""))
        if host in seen_custom:
            continue
        seen_custom.add(host)
        action = str(item.get("action") or "passthrough_cf").strip()
        if action not in {"terminate", "passthrough_cf"}:
            raise SyncError(f"Invalid custom domain action {action!r} for {host}")
        cert_pem = str(item.get("cert_pem") or "").strip()
        key_pem = str(item.get("key_pem") or "").strip()
        raw_origin_sni = str(item.get("origin_sni") or "").strip()
        origin_sni = validate_hostname(raw_origin_sni) if raw_origin_sni else fallback_origin
        if action == "terminate" and (not cert_pem or not key_pem):
            action = "passthrough_cf"
        custom_domains.append(
            {
                "hostname": host,
                "action": action,
                "origin_sni": origin_sni,
                "cert_pem": cert_pem + "\n" if cert_pem else "",
                "key_pem": key_pem + "\n" if key_pem else "",
            }
        )

    cert_files: dict[str, tuple[str, str]] = {}
    site_configs: dict[str, str] = {}
    stream_lines = [
        "# Generated by sync-edge.py. Do not edit manually.",
        f"map $ssl_preread_server_name $adty_cf_passthrough_target {{",
        f"    default {cf_target}:443;",
        "}",
        "",
        "map $ssl_preread_server_name $adty_edge_backend {",
        "    default 127.0.0.1:8444;",
    ]
    http80_lines = [
        "# Generated by sync-edge.py. Do not edit manually.",
        "map $host $adty_http80_allowed {",
        "    default 0;",
    ]

    if edge_enabled:
        for root in platform_roots:
            escaped_root = escape_nginx_regex(root)
            # Explicitly reject api-sN.<root> and collision-suffixed api-sN-M.<root> even if accidentally pointed here.
            stream_lines.append(f"    ~*^api-s[0-9]+(-[0-9]+)?\\.{escaped_root}$ 127.0.0.1:8444;")
            http80_lines.append(f"    ~*^api-s[0-9]+(-[0-9]+)?\\.{escaped_root}$ 0;")
            if root in wildcard_by_root:
                slug = f"wildcard_{safe_file_slug(root)}"
                cert_files[slug] = (
                    wildcard_by_root[root]["cert_pem"],
                    wildcard_by_root[root]["key_pem"],
                )
                stream_lines.append(f"    ~*^[a-z0-9-]+\\.{escaped_root}$ 127.0.0.1:8443;")
                http80_lines.append(f"    ~*^[a-z0-9-]+\\.{escaped_root}$ 1;")
                site_configs[f"sites/{slug}.conf"] = (
                    f"# Wildcard platform website block for *.{root}\n"
                    "server {\n"
                    "    listen 127.0.0.1:8443 ssl http2 proxy_protocol;\n"
                    f"    server_name ~^(?!api-s[0-9]+(-[0-9]+)?\\.)[a-z0-9-]+\\.{escaped_root}$;\n"
                    f"    ssl_certificate     {certs_dir / (slug + '.pem')};\n"
                    f"    ssl_certificate_key {certs_dir / (slug + '.key')};\n"
                    "    ssl_protocols       TLSv1.2 TLSv1.3;\n"
                    "    ssl_session_cache   shared:ADTY_SSL:16m;\n"
                    "    ssl_session_timeout 1d;\n"
                    f"    set $adty_origin_sni \"{fallback_origin}\";\n"
                    f"    include {cfg.conf_dir / 'origin-proxy.conf'};\n"
                    "}\n"
                )
            else:
                stream_lines.append(f"    ~*^[a-z0-9-]+\\.{escaped_root}$ 127.0.0.1:8445;")

        for dom in custom_domains:
            host = dom["hostname"]
            http80_lines.append(f"    {host} 1;")
            if dom["action"] == "terminate":
                slug = f"custom_{safe_file_slug(host)}"
                cert_files[slug] = (dom["cert_pem"], dom["key_pem"])
                stream_lines.append(f"    {host} 127.0.0.1:8443;")
                site_configs[f"sites/{slug}.conf"] = (
                    f"# Custom website block for {host}\n"
                    "server {\n"
                    "    listen 127.0.0.1:8443 ssl http2 proxy_protocol;\n"
                    f"    server_name {host};\n"
                    f"    ssl_certificate     {certs_dir / (slug + '.pem')};\n"
                    f"    ssl_certificate_key {certs_dir / (slug + '.key')};\n"
                    "    ssl_protocols       TLSv1.2 TLSv1.3;\n"
                    "    ssl_session_cache   shared:ADTY_SSL:16m;\n"
                    "    ssl_session_timeout 1d;\n"
                    f"    set $adty_origin_sni \"{dom['origin_sni']}\";\n"
                    f"    include {cfg.conf_dir / 'origin-proxy.conf'};\n"
                    "}\n"
                )
            else:
                stream_lines.append(f"    {host} 127.0.0.1:8445;")

    apex_host, apex_target = normalize_apex_redirect(cfg)
    http80_extra_configs: dict[str, str] = {}
    if apex_host:
        if apex_host in seen_custom:
            print(
                f"adty-edge-sync: skipping APEX_REDIRECT_HOST={apex_host} because it is already in manifest custom_domains",
                file=sys.stderr,
            )
        else:
            apex_slug = f"apex_{safe_file_slug(apex_host)}"
            http80_extra_configs[f"http80-extra/{apex_slug}.conf"] = (
                f"# Apex HTTP-01 challenge and direct 301 redirect for {apex_host}\n"
                "server {\n"
                "    listen 80;\n"
                "    listen [::]:80;\n"
                f"    server_name {apex_host};\n"
                "\n"
                "    location ^~ /.well-known/acme-challenge/ {\n"
                f"        root {cfg.apex_webroot};\n"
                "        default_type text/plain;\n"
                "        try_files $uri =404;\n"
                "    }\n"
                "\n"
                "    location / {\n"
                f"        return 301 {apex_target}$request_uri;\n"
                "    }\n"
                "}\n"
            )
            if apex_cert_present(cfg, apex_host):
                apex_cert, apex_key = apex_cert_paths(cfg, apex_host)
                stream_lines.append(f"    {apex_host} 127.0.0.1:8443;")
                site_configs[f"sites/{apex_slug}.conf"] = (
                    f"# Apex HTTPS 301 redirect block for {apex_host}\n"
                    "server {\n"
                    "    listen 127.0.0.1:8443 ssl http2 proxy_protocol;\n"
                    f"    server_name {apex_host};\n"
                    f"    ssl_certificate     {apex_cert};\n"
                    f"    ssl_certificate_key {apex_key};\n"
                    "    ssl_protocols       TLSv1.2 TLSv1.3;\n"
                    "    ssl_session_cache   shared:ADTY_SSL:16m;\n"
                    "    ssl_session_timeout 1d;\n"
                    "\n"
                    "    if ($host != $ssl_server_name) {\n"
                    "        return 421;\n"
                    "    }\n"
                    "\n"
                    '    add_header Strict-Transport-Security "max-age=31536000" always;\n'
                    f"    return 301 {apex_target}$request_uri;\n"
                    "}\n"
                )

    stream_lines.append("}")
    http80_lines.append("}")

    upstream_conf = (
        "# Generated by sync-edge.py. Do not edit manually.\n"
        "upstream adty_origin {\n"
        f"    server {origin_upstream_host}:443 max_conns=200;\n"
        "    keepalive 32;\n"
        "}\n"
    )

    files: dict[str, str] = {
        "stream-map.conf": "\n".join(stream_lines) + "\n",
        "http80-hosts.conf": "\n".join(http80_lines) + "\n",
        "origin-upstream.conf": upstream_conf,
    }
    files.update(site_configs)
    files.update(http80_extra_configs)
    active_count = len(wildcard_by_root) + len(custom_domains) if edge_enabled else 0
    return files, cert_files, active_count


def load_state(state_path: pathlib.Path) -> dict[str, Any]:
    if not state_path.exists():
        return {}
    try:
        data = json.loads(state_path.read_text(encoding="utf-8"))
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}


def save_state(state_path: pathlib.Path, state: dict[str, Any]) -> None:
    tmp = state_path.with_suffix(".tmp")
    tmp.write_text(json.dumps(state, indent=2) + "\n", encoding="utf-8")
    os.chmod(tmp, 0o600)
    os.replace(tmp, state_path)


def write_cert_pair(certs_dir: pathlib.Path, slug: str, cert_pem: str, key_pem: str) -> None:
    certs_dir.mkdir(parents=True, exist_ok=True)
    cert_path = certs_dir / f"{slug}.pem"
    key_path = certs_dir / f"{slug}.key"
    for path, content, mode in ((cert_path, cert_pem, 0o644), (key_path, key_pem, 0o600)):
        if path.exists() and path.read_text(encoding="utf-8") == content:
            os.chmod(path, mode)
            continue
        fd, tmp_name = tempfile.mkstemp(dir=certs_dir, prefix=f".{slug}.")
        try:
            with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as fh:
                fh.write(content)
            os.chmod(tmp_name, mode)
            os.replace(tmp_name, path)
        except Exception:
            if os.path.exists(tmp_name):
                os.unlink(tmp_name)
            raise


def prune_expired_certs(
    certs_dir: pathlib.Path, active_slugs: set[str], retention_days: int, now: float
) -> None:
    if not certs_dir.exists():
        return
    cutoff = now - retention_days * 86400
    for entry in certs_dir.iterdir():
        if entry.suffix not in {".pem", ".key"}:
            continue
        if entry.stem in active_slugs:
            continue
        try:
            if entry.stat().st_mtime < cutoff:
                entry.unlink()
        except OSError:
            pass


def _copy_cert_file(src: str | os.PathLike[str], dst: str | os.PathLike[str]) -> str:
    src_path = pathlib.Path(src)
    dst_path = pathlib.Path(dst)
    create_mode = 0o600 if src_path.suffix == ".key" else 0o644
    flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC | getattr(os, "O_BINARY", 0)
    fd = os.open(dst_path, flags, create_mode)
    try:
        with os.fdopen(fd, "wb") as out_fh, src_path.open("rb") as in_fh:
            shutil.copyfileobj(in_fh, out_fh)
    except Exception:
        if dst_path.exists():
            dst_path.unlink()
        raise
    shutil.copystat(src_path, dst_path)
    if src_path.suffix == ".key":
        os.chmod(dst_path, 0o600)
    return str(dst_path)


def apply_and_reload(
    cfg: EdgeConfig,
    rendered_files: dict[str, str],
    cert_files: dict[str, tuple[str, str]],
    now: float | None = None,
    reload_nginx: bool = True,
) -> None:
    now_ts = time.time() if now is None else now
    cfg.conf_dir.mkdir(parents=True, exist_ok=True)
    certs_dir = cfg.conf_dir / "certs"
    sites_dir = cfg.conf_dir / "sites"
    http80_extra_dir = cfg.conf_dir / "http80-extra"
    certs_dir.mkdir(parents=True, exist_ok=True)
    sites_dir.mkdir(parents=True, exist_ok=True)
    http80_extra_dir.mkdir(parents=True, exist_ok=True)

    backup_dir = pathlib.Path(tempfile.mkdtemp(prefix="adty-edge-backup-"))
    managed_top_files = ("stream-map.conf", "http80-hosts.conf", "origin-upstream.conf")

    def _rollback_snapshot() -> None:
        for name in managed_top_files:
            bak = backup_dir / name
            dest = cfg.conf_dir / name
            if bak.exists():
                shutil.copy2(bak, dest)
            elif dest.exists():
                dest.unlink()
        shutil.rmtree(sites_dir, ignore_errors=True)
        shutil.copytree(backup_dir / "sites", sites_dir)
        shutil.rmtree(http80_extra_dir, ignore_errors=True)
        shutil.copytree(backup_dir / "http80-extra", http80_extra_dir)
        shutil.rmtree(certs_dir, ignore_errors=True)
        shutil.copytree(backup_dir / "certs", certs_dir, copy_function=_copy_cert_file)
        for key_file in certs_dir.rglob("*.key"):
            if key_file.is_file():
                os.chmod(key_file, 0o600)

    snapshot_ready = False
    try:
        # Snapshot existing generated configs and certs for atomic rollback before writing anything.
        for name in managed_top_files:
            src = cfg.conf_dir / name
            if src.exists():
                shutil.copy2(src, backup_dir / name)
        shutil.copytree(sites_dir, backup_dir / "sites")
        shutil.copytree(http80_extra_dir, backup_dir / "http80-extra")
        shutil.copytree(certs_dir, backup_dir / "certs", copy_function=_copy_cert_file)
        snapshot_ready = True

        # Write updated certs and configs into place, then validate and reload.
        # Any exception or non-zero exit before nginx validates and reloads
        # cleanly rolls back both nginx configs and certs_dir.
        try:
            for slug, (cert_pem, key_pem) in cert_files.items():
                write_cert_pair(certs_dir, slug, cert_pem, key_pem)

            for rel, content in rendered_files.items():
                dest = cfg.conf_dir / rel
                dest.parent.mkdir(parents=True, exist_ok=True)
                tmp = dest.with_suffix(dest.suffix + ".tmp")
                tmp.write_text(content, encoding="utf-8", newline="\n")
                os.chmod(tmp, 0o644)
                os.replace(tmp, dest)

            # Remove stale site and HTTP-80 extra files not in the new render.
            wanted_sites = {
                pathlib.Path(rel).name for rel in rendered_files if rel.startswith("sites/")
            }
            for existing in list(sites_dir.glob("*.conf")):
                if existing.name not in wanted_sites:
                    existing.unlink()

            wanted_http80_extra = {
                pathlib.Path(rel).name for rel in rendered_files if rel.startswith("http80-extra/")
            }
            for existing in list(http80_extra_dir.glob("*.conf")):
                if existing.name not in wanted_http80_extra:
                    existing.unlink()

            test_proc = subprocess.run(
                [cfg.nginx_bin, "-t"],
                capture_output=True,
                text=True,
                check=False,
            )
            if test_proc.returncode != 0:
                stderr = (test_proc.stderr or test_proc.stdout or "").strip()
                raise SyncError(f"nginx -t failed; rolled back previous config: {stderr}")

            if reload_nginx:
                reload_proc = subprocess.run(
                    shlex.split(cfg.nginx_reload_cmd),
                    capture_output=True,
                    text=True,
                    check=False,
                )
                if reload_proc.returncode != 0:
                    stderr = (reload_proc.stderr or reload_proc.stdout or "").strip()
                    raise SyncError(f"nginx reload failed; rolled back previous config: {stderr}")
        except BaseException:
            if snapshot_ready:
                _rollback_snapshot()
            raise

        prune_expired_certs(certs_dir, set(cert_files.keys()), cfg.cert_retention_days, now_ts)
    finally:
        shutil.rmtree(backup_dir, ignore_errors=True)


def run_sync(cfg: EdgeConfig, reload_nginx: bool = True) -> str:
    state_path = cfg.conf_dir / ".manifest-state.json"
    state = load_state(state_path)
    prev_etag = str(state.get("etag") or "")
    prev_count = int(state.get("domain_count") or 0)
    prev_apex_fp = str(state.get("apex_fingerprint") or "")
    apex_fp = compute_apex_fingerprint(cfg)

    fetch_etag = prev_etag if apex_fp == prev_apex_fp else ""
    status, etag, manifest = fetch_manifest(cfg, etag=fetch_etag)
    updated = False
    active_count = prev_count

    if status != 304 and manifest is not None:
        certs_dir = cfg.conf_dir / "certs"
        rendered_files, cert_files, active_count = render_configs(manifest, cfg, certs_dir)

        edge_enabled = bool(manifest.get("edge_mainland_enabled", False))
        if (
            edge_enabled
            and not cfg.allow_mass_drop
            and prev_count >= 10
            and active_count < int(prev_count * cfg.min_drop_ratio)
        ):
            raise SyncError(
                f"Refusing mass domain drop from {prev_count} to {active_count}; set ALLOW_MASS_DROP=1 to override"
            )

        apply_and_reload(cfg, rendered_files, cert_files, reload_nginx=reload_nginx)
        save_state(
            state_path,
            {
                "etag": etag or f'"{manifest.get("version", "")}"',
                "version": str(manifest.get("version") or ""),
                "domain_count": active_count,
                "apex_fingerprint": apex_fp,
                "synced_at": int(time.time()),
            },
        )
        updated = True

    if reload_nginx and cfg.apex_redirect_host.strip():
        try:
            ensure_apex_cert(cfg)
        except Exception as exc:
            print(f"adty-edge-sync: apex cert check failed: {exc}", file=sys.stderr)
        new_apex_fp = compute_apex_fingerprint(cfg)
        if new_apex_fp != apex_fp:
            if manifest is None:
                status, etag, manifest = fetch_manifest(cfg, etag="")
                if status == 304 or manifest is None:
                    return "updated" if updated else "unchanged"
            certs_dir = cfg.conf_dir / "certs"
            rendered_files, cert_files, active_count = render_configs(manifest, cfg, certs_dir)
            apply_and_reload(cfg, rendered_files, cert_files, reload_nginx=True)
            save_state(
                state_path,
                {
                    "etag": etag or f'"{manifest.get("version", "")}"',
                    "version": str(manifest.get("version") or ""),
                    "domain_count": active_count,
                    "apex_fingerprint": new_apex_fp,
                    "synced_at": int(time.time()),
                },
            )
            updated = True

    return "updated" if updated else "unchanged"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Sync ADTY AI edge Nginx manifest.")
    parser.add_argument(
        "--no-reload",
        action="store_true",
        help="Validate generated config with `nginx -t` without reloading a running Nginx master.",
    )
    args = parser.parse_args(argv)
    try:
        cfg = EdgeConfig.from_env()
        result = run_sync(cfg, reload_nginx=not args.no_reload)
        print(f"adty-edge-sync: {result}")
        return 0
    except Exception as exc:
        print(f"adty-edge-sync: ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
