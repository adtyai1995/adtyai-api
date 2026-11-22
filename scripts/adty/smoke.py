#!/usr/bin/env python3
"""L0 串行冒烟；只经真实 API 写业务数据，PostgreSQL 仅作只读断言。

从 WSL 原生源码副本运行 bash scripts/adty/smoke.sh。必填环境变量：
ADTY_SMOKE_BASE_URL（本机隔离 API），ADTY_TEST_PG_DSN，
ADTY_SMOKE_DATABASE（adty_smoke_ 或 adty_browser_ 开头的独立库），
ADTY_SMOKE_REDIS_PORT，ADTY_SMOKE_ROOT_USERNAME，ADTY_SMOKE_ROOT_PASSWORD，
ADTY_SMOKE_SOURCE_COMMIT，ADTY_SMOKE_SOURCE_SNAPSHOT_ID，ADTY_SMOKE_BUILD_ID。
ADTY_SMOKE_ARTIFACT_DIR 指向仓库外且尚不存在的本轮目录。
ADTY_SMOKE_SERVICE_MANIFEST 指向隔离服务启动记录，包含相同源码/构建身份、
base_url、pid、executable_sha256、frontend_build_sha256。
分站参数、注册方式和 Root 账号由隔离环境通过现有正式入口预先准备。
脚本不改安全开关，不启用支付通道，不调用外部网关，不清理失败现场。
"""
import argparse
import datetime
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


class SmokeFailure(Exception):
    """可脱敏归档的 L0 契约失败。"""


def require_success(status, body):
    if status != 200 or not isinstance(body, dict) or body.get("success") is not True:
        raise SmokeFailure("HTTP 状态或业务 success 不符合成功契约")
    return body.get("data")


def require_auth_refusal(status, body):
    if status not in {401, 403} or not isinstance(body, dict) or body.get("success") is not False:
        raise SmokeFailure("登出后未得到明确鉴权拒绝；服务器错误不计拒绝成功")


def session_headers(data):
    if (not isinstance(data, dict) or not isinstance(data.get("access_token"), str)
            or not data["access_token"] or not isinstance(data.get("token_type"), str)
            or data["token_type"].lower() != "bearer"
            or not isinstance(data.get("session"), dict) or not isinstance(data["session"].get("sid"), str)
            or not data["session"]["sid"]
            or not isinstance(data.get("user"), dict) or type(data["user"].get("id")) is not int
            or data["user"]["id"] <= 0):
        raise SmokeFailure("登录未返回完整有效会话，挑战不计登录成功")
    return {"Authorization": "Bearer " + data["access_token"], "X-Auth-Session": data["session"]["sid"]}


def local_origin(value):
    parsed = urllib.parse.urlsplit(value)
    if (parsed.scheme not in {"http", "https"} or parsed.hostname not in {"localhost", "127.0.0.1", "::1"}
            or parsed.username is not None or parsed.password is not None
            or parsed.path not in {"", "/"} or parsed.query or parsed.fragment):
        raise SmokeFailure("隔离 API 必须是无凭据的本机 HTTP(S) Origin")
    return value.rstrip("/")


def owns_listener(process, base):
    address = urllib.parse.urlsplit(base)
    port = address.port or (443 if address.scheme == "https" else 80)
    addresses = set()
    for family, _, _, _, endpoint in socket.getaddrinfo(address.hostname, port, type=socket.SOCK_STREAM):
        addresses.add(socket.inet_pton(family, endpoint[0]))
    sockets = set()
    for descriptor in (process / "fd").iterdir():
        try:
            target = os.readlink(descriptor)
        except FileNotFoundError:
            continue
        if match := re.fullmatch(r"socket:\[(\d+)\]", target):
            sockets.add(match[1])
    for table in ("tcp", "tcp6"):
        for line in (process / "net" / table).read_text().splitlines()[1:]:
            cells = line.split()
            if cells[3] != "0A" or cells[9] not in sockets:
                continue
            host_hex, port_hex = cells[1].split(":")
            host = b"".join(int(host_hex[i:i + 8], 16).to_bytes(4, sys.byteorder)
                            for i in range(0, len(host_hex), 8))
            if int(port_hex, 16) == port and host in addresses:
                return True
    return False


def run_steps(steps, directory):
    rows = []
    failed = False
    for case_id, action in steps:
        row = {"schema_version": "2.0", "case_id": case_id, "outcome": "unexecuted", "exit_code": None}
        if not failed:
            row["started_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
            started = time.monotonic()
            try:
                result = action() or {}
                row.update(result)
                row.setdefault("exit_code", 0)
                if row["exit_code"] is None:
                    row["exit_code"] = 0
                row["outcome"] = "passed" if row["exit_code"] == 0 else "failed"
            except (SmokeFailure, OSError, ValueError, TypeError, KeyError, AttributeError, subprocess.SubprocessError) as exc:
                row.update(outcome="failed", exit_code=1, reason=str(exc) if isinstance(exc, SmokeFailure) else type(exc).__name__)
            row["ended_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
            row["duration_seconds"] = round(time.monotonic() - started, 6)
            failed = row["outcome"] != "passed"
        rows.append(row)
        (directory / "attempts.jsonl").write_text("".join(json.dumps(item, ensure_ascii=False) + "\n" for item in rows))
        print(case_id + ": " + row["outcome"], flush=True)
    return rows


class RejectRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise SmokeFailure("冒烟 API 不接受重定向")


class Runtime:
    def __init__(self, repo, artifacts):
        self.repo = repo
        self.artifacts = artifacts
        # 服务配置在 S-05 以后各自校验，不能阻止无服务依赖的 S-01—S-04。
        self.base = os.environ.get("ADTY_SMOKE_BASE_URL", "")
        self.dsn = os.environ.get("ADTY_TEST_PG_DSN", "")
        self.database = os.environ.get("ADTY_SMOKE_DATABASE", "")
        self.cookies = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(self.cookies), RejectRedirects())
        self.headers = {}
        self.deadline = None
        self.fixture = {}
        self.http_events = []

    def api(self, method, path, data=None, expect_success=True):
        self.base = local_origin(self.base)
        timeout = max(0.01, self.deadline - time.monotonic()) if self.deadline else 10
        if self.deadline and timeout <= 0.01:
            raise SmokeFailure("业务冒烟超过该项总超时")
        request = urllib.request.Request(self.base + path, method=method,
                                        data=json.dumps(data).encode() if data is not None else None,
                                        headers={"Content-Type": "application/json", "Origin": self.base, **self.headers})
        try:
            response = self.opener.open(request, timeout=min(timeout, 10))
        except urllib.error.HTTPError as exc:
            response = exc
        with response:
            status = response.status
            try:
                body = json.load(response)
            except ValueError:
                body = None
        # 只记结构事实，绝不归档密码、cookie、访问令牌或支付密钥。
        self.http_events.append({"method": method, "path": path, "status": status,
                                 "success": body.get("success") if isinstance(body, dict) else None})
        (self.artifacts / "http.json").write_text(json.dumps(self.http_events, ensure_ascii=False, indent=2) + "\n")
        if expect_success:
            return require_success(status, body)
        return status, body

    def postgres(self, query):
        if not query.startswith(("SELECT ", "SHOW ")) or ";" in query:
            raise SmokeFailure("冒烟数据库连接仅允许单条只读断言")
        result = subprocess.run(["psql", self.dsn, "-XAt", "-v", "ON_ERROR_STOP=1", "-c", query],
                                text=True, capture_output=True, timeout=10)
        if result.returncode:
            raise SmokeFailure("隔离 PostgreSQL 只读断言失败；未保存含连接信息的错误")
        return result.stdout.strip()

    def command(self, case_id, command, module="."):
        path = self.artifacts / (case_id + ".log")
        with path.open("wb") as output:
            result = subprocess.run(command, cwd=self.repo / module, stdout=output, stderr=subprocess.STDOUT)
        return {"command": command, "module": module, "exit_code": result.returncode,
                "log": path.name, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}

    def lint(self):
        report = self.artifacts / "S-04-oxlint.json"
        result = self.command("S-04", ["node", "scripts/adty/ratchet.mjs", "lint", "--report", str(report.resolve())])
        if report.is_file():
            result.update(report=report.name, report_sha256=hashlib.sha256(report.read_bytes()).hexdigest())
        elif result["exit_code"] == 0:
            raise SmokeFailure("S-04 缺少本次 oxlint 原始诊断")
        return result

    def frontend_build(self):
        result = self.command("S-03", ["bun", "run", "build"], "web")
        if result["exit_code"]:
            return result
        dist = self.repo / "web" / "dist"
        index = dist / "index.html"
        if not index.is_file() or not index.stat().st_size:
            raise SmokeFailure("S-03 缺少实际前端入口")
        digest = hashlib.sha256()
        for path in sorted(p for p in dist.rglob("*") if p.is_file()):
            digest.update(path.relative_to(dist).as_posix().encode() + b"\0")
            with path.open("rb") as source:
                digest.update(hashlib.file_digest(source, "sha256").digest())
        self.frontend_sha256 = digest.hexdigest()
        return {**result, "frontend_build_sha256": self.frontend_sha256}

    def candidate_health(self, identity):
        manifest_path = os.environ.get("ADTY_SMOKE_SERVICE_MANIFEST")
        if not manifest_path:
            raise SmokeFailure("缺少隔离服务启动记录 ADTY_SMOKE_SERVICE_MANIFEST")
        manifest = json.loads(Path(manifest_path).read_bytes())
        base = local_origin(self.base)
        if (any(manifest.get(k) != v for k, v in identity.items()) or manifest.get("base_url") != base
                or manifest.get("frontend_build_sha256") != self.frontend_sha256):
            raise SmokeFailure("服务启动记录与当前候选或前端构建不一致")
        pid = manifest.get("pid")
        if type(pid) is not int or pid <= 0:
            raise SmokeFailure("隔离服务 PID 无效")
        process = Path("/proc") / str(pid)
        with (process / "exe").open("rb") as executable:
            actual = hashlib.file_digest(executable, "sha256").hexdigest()
        if actual != manifest.get("executable_sha256"):
            raise SmokeFailure("隔离进程可执行文件与启动记录不一致")
        if not owns_listener(process, base):
            raise SmokeFailure("隔离服务进程未监听记录中的本机地址和端口")
        self.health()
        return {**identity, "pid": pid, "base_url": base, "executable_sha256": actual,
                "frontend_build_sha256": self.frontend_sha256}

    def postgres_ready(self):
        if not re.fullmatch(r"adty_(smoke|browser)_[a-z0-9_]+", self.database) or not self.dsn:
            raise SmokeFailure("必须明确指定独立冒烟数据库及真实 PostgreSQL DSN")
        result = self.postgres("SELECT current_database() || '|' || current_setting('server_version_num') || '|' || 1")
        pieces = result.split("|")
        if len(pieces) != 3 or pieces[0] != self.database or not pieces[1].isdigit() or int(pieces[1]) // 10000 != 18 or pieces[2] != "1":
            raise SmokeFailure("连接身份、PostgreSQL 18 或 SELECT 1 不符合隔离契约")
        return {"database": self.database, "server_version_num": pieces[1], "exit_code": 0}

    def redis_ready(self):
        port = os.environ.get("ADTY_SMOKE_REDIS_PORT", "")
        if not port.isdigit() or not 1 <= int(port) <= 65535:
            raise SmokeFailure("必须指定本轮隔离 Redis 端口")
        result = subprocess.run(["redis-cli", "-h", "127.0.0.1", "-p", port, "PING"], capture_output=True, text=True, timeout=5)
        if result.returncode or result.stdout.strip() != "PONG":
            raise SmokeFailure("隔离 Redis 未返回 PONG")
        return {"exit_code": 0, "response": "PONG"}

    def health(self):
        # 严格保留 F.1.1 的路径；实际 /api/ready 不能静默顶替此项。
        self.api("GET", "/api/health")

    def login(self, username, password):
        if not username or not password:
            raise SmokeFailure("缺少本轮合成账号凭据")
        key = self.api("GET", "/api/user/login/encryption-key")
        payload = {"username": username}
        if isinstance(key, dict) and key.get("enabled") is False:
            payload["password"] = password
        else:
            if not isinstance(key, dict) or not key.get("kid") or not key.get("public_key"):
                raise SmokeFailure("登录加密公钥不可用")
            script = "const c=require('node:crypto');let s='';process.stdin.on('data',x=>s+=x);process.stdin.on('end',()=>{const x=JSON.parse(s);process.stdout.write(c.publicEncrypt({key:x.key,oaepHash:'sha256',padding:c.constants.RSA_PKCS1_OAEP_PADDING},Buffer.from(x.password)).toString('base64'))})"
            result = subprocess.run(["node", "-e", script], input=json.dumps({"key": key["public_key"], "password": password}),
                                    capture_output=True, text=True, timeout=10)
            if result.returncode or not result.stdout:
                raise SmokeFailure("本机登录密码加密失败")
            payload.update(password_encrypted=result.stdout, encryption_key_id=key["kid"])
        bundle = self.api("POST", "/api/user/login", payload)
        self.headers = session_headers(bundle)
        if not any(cookie.name == "adtyai_refresh" for cookie in self.cookies):
            raise SmokeFailure("登录未设置刷新 cookie")
        return bundle

    def auth_lifecycle(self):
        suffix = secrets.token_hex(5)
        username, password = "smk" + suffix, "LocalGate!" + secrets.token_hex(12)
        self.api("POST", "/api/user/register", {"username": username, "password": password, "agreement_accepted": True})
        user_id = self.postgres("SELECT id FROM users WHERE username = '" + username + "' AND site_id = 0")
        if not user_id.isdigit() or int(user_id) <= 0:
            raise SmokeFailure("注册未持久化独立主站用户")
        first = self.login(username, password)
        if first["user"]["id"] != int(user_id):
            raise SmokeFailure("登录返回账号与注册记录不一致")
        old_token = first["access_token"]
        old_cookie = next(cookie.value for cookie in self.cookies if cookie.name == "adtyai_refresh")
        refreshed = self.api("POST", "/api/user/auth/refresh")
        self.headers = session_headers(refreshed)
        sid = self.headers["X-Auth-Session"]
        if refreshed["user"]["id"] != int(user_id) or refreshed["access_token"] == old_token or sid != first["session"]["sid"]:
            raise SmokeFailure("刷新未轮换令牌或改变了会话归属")
        new_cookies = [cookie.value for cookie in self.cookies if cookie.name == "adtyai_refresh"]
        if len(new_cookies) != 1 or new_cookies[0] == old_cookie:
            raise SmokeFailure("刷新未轮换刷新 cookie")
        logout = self.api("POST", "/api/user/auth/logout")
        if not isinstance(logout, dict) or logout.get("revoked_sid") != sid or logout.get("cookie_cleared") is not True:
            raise SmokeFailure("登出未确认撤销原会话和清除 cookie")
        if any(cookie.name == "adtyai_refresh" for cookie in self.cookies):
            raise SmokeFailure("登出响应未实际清除客户端刷新 cookie")
        if not re.fullmatch(r"[A-Za-z0-9_-]+", sid):
            raise SmokeFailure("会话 SID 格式不符合只读核对条件")
        if self.postgres("SELECT status FROM user_sessions WHERE sid = '" + sid + "' AND user_id = " + user_id) != "revoked":
            raise SmokeFailure("登出未持久化会话撤销")
        status, body = self.api("GET", "/api/user/self", expect_success=False)
        require_auth_refusal(status, body)
        self.headers = {}
        self.fixture.update(user_id=int(user_id), username=username, suffix=suffix)
        return {"exit_code": 0, "user_id": int(user_id), "refresh_rotated": True, "logout_revoked": True}

    def create_site(self):
        root = self.login(os.environ.get("ADTY_SMOKE_ROOT_USERNAME"), os.environ.get("ADTY_SMOKE_ROOT_PASSWORD"))
        if root["user"].get("role") != 100:
            raise SmokeFailure("创建分站必须使用本轮合成 Root")
        config = self.api("GET", "/api/adty/admin/reseller/settings")
        if not isinstance(config, dict) or not config.get("root_domains"):
            raise SmokeFailure("隔离环境尚未通过正式入口准备分站参数")
        label = "smoke-" + self.fixture["suffix"]
        site = self.api("POST", "/api/adty/admin/sites", {"user_id": self.fixture["user_id"], "subdomain": label, "root_domain": config["root_domains"][0]})
        if not isinstance(site, dict) or type(site.get("id")) is not int or site["id"] <= 0:
            raise SmokeFailure("创建分站未返回有效 ID")
        site_id = str(site["id"])
        if self.postgres("SELECT owner_user_id::text || '|' || subdomain FROM adty_sites WHERE id = " + site_id) != str(self.fixture["user_id"]) + "|" + label:
            raise SmokeFailure("创建分站的数据库归属与响应不一致")
        if self.postgres("SELECT string_agg(kind, ',' ORDER BY kind) FROM adty_site_wallets WHERE site_id = " + site_id) != "earnings,margin,operating":
            raise SmokeFailure("创建分站未持久化三个内部钱包")
        self.fixture["site_id"] = site["id"]
        return {"exit_code": 0, "site_id": site["id"], "owner_user_id": self.fixture["user_id"]}

    def save_epay(self):
        name = "Smoke " + self.fixture["suffix"]
        merchant = "SMOKE_" + self.fixture["suffix"]
        channel = self.api("POST", "/api/adty/admin/epay/channels", {
            "name": name, "gateway_url": self.base + "/synthetic-epay", "merchant_id": merchant,
            "merchant_key": secrets.token_hex(24), "weight": 1, "order_mode": "api", "probe_mode": "off",
            "methods": [{"method": "alipay", "type_code": "alipay", "min_cny": "0", "max_cny": "0", "daily_limit_cny": "0", "enabled": True, "return_type": "payurl"}],
        })
        if not isinstance(channel, dict) or type(channel.get("id")) is not int or channel["id"] <= 0:
            raise SmokeFailure("保存支付通道未返回有效 ID")
        channel_id = str(channel["id"])
        if self.postgres("SELECT name || '|' || merchant_id || '|' || status FROM adty_epay_channels WHERE id = " + channel_id) != name + "|" + merchant + "|disabled":
            raise SmokeFailure("支付通道非敏感配置未正确持久化")
        if self.postgres("SELECT count(*) FROM adty_epay_channel_methods WHERE channel_id = " + channel_id + " AND method = 'alipay' AND enabled") != "1":
            raise SmokeFailure("支付方式未正确关联到新通道")
        self.api("POST", "/api/user/auth/logout")
        self.headers = {}
        return {"exit_code": 0, "channel_id": channel["id"], "status": "disabled"}

    def timed(self, action, seconds):
        self.deadline = time.monotonic() + seconds
        old_handler = signal.getsignal(signal.SIGALRM)

        def expired(signum, frame):
            raise SmokeFailure("该项超过方案总超时")

        signal.signal(signal.SIGALRM, expired)
        signal.setitimer(signal.ITIMER_REAL, seconds)
        try:
            return action()
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)
            signal.signal(signal.SIGALRM, old_handler)
            self.deadline = None


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--list", action="store_true", help="仅列出 S-01—S-10，不执行，不记通过")
    args = parser.parse_args()
    if args.list:
        print("S-03 bun run build\nS-01 go build ./...\nS-02 go vet ./...\nS-04 node scripts/adty/ratchet.mjs lint\nS-05 PostgreSQL 18 SELECT 1\nS-06 Redis PING\nS-07 GET /api/health\nS-08 注册/登录/刷新/登出\nS-09 Root 创建分站并核对数据库\nS-10 保存易支付通道并核对数据库")
        return 0
    repo = Path(__file__).resolve().parents[2]
    if repo.is_relative_to(Path("/mnt")) or os.name != "posix":
        raise SmokeFailure("只能在 WSL/Linux 原生目录的源码副本执行")
    identity = {key: os.environ.get("ADTY_SMOKE_" + key.upper()) for key in ("source_commit", "source_snapshot_id", "build_id")}
    if not all(identity.values()) or not re.fullmatch(r"[0-9a-f]{40}", identity["source_commit"]):
        raise SmokeFailure("缺少实际源码提交、快照或构建身份")
    raw_directory = os.environ.get("ADTY_SMOKE_ARTIFACT_DIR", "")
    if not raw_directory:
        raise SmokeFailure("必须指定仓库外本轮证据目录")
    directory = Path(raw_directory).expanduser().resolve()
    if directory.is_relative_to(repo) or directory.is_relative_to(Path("/mnt")) or directory.exists():
        raise SmokeFailure("证据目录必须位于仓库外原生目录且尚不存在")
    directory.mkdir(parents=True)
    runtime = Runtime(repo, directory)
    steps = [
        ("S-03", runtime.frontend_build),
        ("S-01", lambda: runtime.command("S-01", ["go", "build", "./..."])),
        ("S-02", lambda: runtime.command("S-02", ["go", "vet", "./..."])),
        ("S-04", runtime.lint),
        ("S-05", lambda: runtime.timed(runtime.postgres_ready, 10)),
        ("S-06", lambda: runtime.timed(runtime.redis_ready, 5)),
        ("S-07", lambda: runtime.timed(lambda: runtime.candidate_health(identity), 10)),
        ("S-08", lambda: runtime.timed(runtime.auth_lifecycle, 30)),
        ("S-09", lambda: runtime.timed(runtime.create_site, 15)),
        ("S-10", lambda: runtime.timed(runtime.save_epay, 10)),
    ]
    rows = run_steps(steps, directory)
    report = {"schema_version": "2.0", **identity, "ended_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "result": "passed" if all(row["outcome"] == "passed" for row in rows) else "failed", "attempts": rows,
              "artifacts": [{"path": path.name, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()} for path in sorted(directory.iterdir()) if path.is_file()]}
    (directory / "smoke-report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    return 0 if report["result"] == "passed" else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SmokeFailure as failure:
        print("smoke: " + str(failure), file=sys.stderr)
        sys.exit(1)
