#!/usr/bin/env python3
"""Synchronize ADTY AI mainland edge Nginx configuration and certificates from origin.

Stdlib-only Python 3 script run periodically by adty-edge-sync.timer.
Fetches GET /api/adty/internal/edge/manifest using X-Adty-Edge-Token + ETag/304,
validates hostnames and anti-mass-deletion invariants, writes certificates (0644/0600),
generates Nginx stream/HTTP snippets, validates with `nginx -t` (rolling back on error),
and reloads Nginx only when valid changes were applied.
"""

from __future__ import annotations

import http.client
import json
import os
import pathlib
import re
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
            # Explicitly reject api-sN.<root> even if accidentally pointed here.
            stream_lines.append(f"    ~*^api-s[0-9]+\\.{escaped_root}$ 127.0.0.1:8444;")
            http80_lines.append(f"    ~*^api-s[0-9]+\\.{escaped_root}$ 0;")
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
                    f"    server_name ~^(?!api-s[0-9]+\\.)[a-z0-9-]+\\.{escaped_root}$;\n"
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


def apply_and_reload(
    cfg: EdgeConfig,
    rendered_files: dict[str, str],
    cert_files: dict[str, tuple[str, str]],
    now: float | None = None,
) -> None:
    now_ts = time.time() if now is None else now
    cfg.conf_dir.mkdir(parents=True, exist_ok=True)
    certs_dir = cfg.conf_dir / "certs"
    sites_dir = cfg.conf_dir / "sites"
    sites_dir.mkdir(parents=True, exist_ok=True)

    for slug, (cert_pem, key_pem) in cert_files.items():
        write_cert_pair(certs_dir, slug, cert_pem, key_pem)

    backup_dir = pathlib.Path(tempfile.mkdtemp(prefix="adty-edge-backup-"))
    managed_top_files = ("stream-map.conf", "http80-hosts.conf", "origin-upstream.conf")
    try:
        # Snapshot existing generated configs for atomic rollback if nginx -t fails.
        for name in managed_top_files:
            src = cfg.conf_dir / name
            if src.exists():
                shutil.copy2(src, backup_dir / name)
        shutil.copytree(sites_dir, backup_dir / "sites")

        # Write updated configs into place.
        for rel, content in rendered_files.items():
            dest = cfg.conf_dir / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            tmp = dest.with_suffix(dest.suffix + ".tmp")
            tmp.write_text(content, encoding="utf-8", newline="\n")
            os.chmod(tmp, 0o644)
            os.replace(tmp, dest)

        # Remove stale site files not in the new manifest.
        wanted_sites = {
            pathlib.Path(rel).name for rel in rendered_files if rel.startswith("sites/")
        }
        for existing in list(sites_dir.glob("*.conf")):
            if existing.name not in wanted_sites:
                existing.unlink()

        test_proc = subprocess.run(
            [cfg.nginx_bin, "-t"],
            capture_output=True,
            text=True,
            check=False,
        )
        if test_proc.returncode != 0:
            # Roll back immediately before raising.
            for name in managed_top_files:
                bak = backup_dir / name
                dest = cfg.conf_dir / name
                if bak.exists():
                    shutil.copy2(bak, dest)
                elif dest.exists():
                    dest.unlink()
            shutil.rmtree(sites_dir, ignore_errors=True)
            shutil.copytree(backup_dir / "sites", sites_dir)
            stderr = (test_proc.stderr or test_proc.stdout or "").strip()
            raise SyncError(f"nginx -t failed; rolled back previous config: {stderr}")

        reload_proc = subprocess.run(
            shlex.split(cfg.nginx_reload_cmd),
            capture_output=True,
            text=True,
            check=False,
        )
        if reload_proc.returncode != 0:
            stderr = (reload_proc.stderr or reload_proc.stdout or "").strip()
            raise SyncError(f"nginx reload failed: {stderr}")

        prune_expired_certs(certs_dir, set(cert_files.keys()), cfg.cert_retention_days, now_ts)
    finally:
        shutil.rmtree(backup_dir, ignore_errors=True)


def run_sync(cfg: EdgeConfig) -> str:
    state_path = cfg.conf_dir / ".manifest-state.json"
    state = load_state(state_path)
    prev_etag = str(state.get("etag") or "")
    prev_count = int(state.get("domain_count") or 0)

    status, etag, manifest = fetch_manifest(cfg, etag=prev_etag)
    if status == 304 or manifest is None:
        return "unchanged"

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

    apply_and_reload(cfg, rendered_files, cert_files)
    save_state(
        state_path,
        {
            "etag": etag or f'"{manifest.get("version", "")}"',
            "version": str(manifest.get("version") or ""),
            "domain_count": active_count,
            "synced_at": int(time.time()),
        },
    )
    return "updated"


def main() -> int:
    try:
        cfg = EdgeConfig.from_env()
        result = run_sync(cfg)
        print(f"adty-edge-sync: {result}")
        return 0
    except Exception as exc:
        print(f"adty-edge-sync: ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
