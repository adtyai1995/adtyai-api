"""L0 工具自身的失败屏障；夹具不计为产品冒烟证据。"""
import importlib.util
from pathlib import Path
import tempfile
import unittest


class SmokeContractTests(unittest.TestCase):
    def setUp(self):
        script = Path(__file__).resolve().parents[1] / "smoke.py"
        self.assertTrue(script.is_file(), "缺少方案 F.1.1 的 L0 冒烟实现")
        spec = importlib.util.spec_from_file_location("adty_smoke", script)
        self.smoke = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.smoke)

    def test_http_200_with_business_failure_is_rejected(self):
        with self.assertRaises(self.smoke.SmokeFailure):
            self.smoke.require_success(200, {"success": False})

    def test_server_error_with_success_body_is_rejected(self):
        with self.assertRaises(self.smoke.SmokeFailure):
            self.smoke.require_success(500, {"success": True})

    def test_health_and_configuration_require_exact_http_200(self):
        with self.assertRaises(self.smoke.SmokeFailure):
            self.smoke.require_success(201, {"success": True})

    def test_success_requires_boolean_true(self):
        with self.assertRaises(self.smoke.SmokeFailure):
            self.smoke.require_success(200, {"success": "true"})
        self.assertEqual({"id": 7}, self.smoke.require_success(200, {"success": True, "data": {"id": 7}}))

    def test_login_challenge_cannot_impersonate_completed_session(self):
        with self.assertRaises(self.smoke.SmokeFailure):
            self.smoke.session_headers({"require_verification": True, "flow_token": "fixture"})

    def test_session_requires_token_sid_and_matching_user(self):
        data = {"access_token": "fixture", "token_type": "Bearer", "session": {"sid": "sid"}, "user": {"id": 7}}
        headers = self.smoke.session_headers(data)
        self.assertEqual("Bearer fixture", headers["Authorization"])
        self.assertEqual("sid", headers["X-Auth-Session"])
        del data["session"]["sid"]
        with self.assertRaises(self.smoke.SmokeFailure):
            self.smoke.session_headers(data)

    def test_null_token_type_is_reported_as_invalid_session(self):
        with self.assertRaises(self.smoke.SmokeFailure):
            self.smoke.session_headers({"access_token": "fixture", "token_type": None, "session": {"sid": "sid"}, "user": {"id": 7}})

    def test_server_error_cannot_prove_logout_denied_access(self):
        with self.assertRaises(self.smoke.SmokeFailure):
            self.smoke.require_auth_refusal(500, {"success": False})
        self.smoke.require_auth_refusal(401, {"success": False})

    def test_missing_service_configuration_does_not_prevent_command_stages(self):
        import os
        import sys
        with tempfile.TemporaryDirectory(prefix="adty-smoke-contract-") as directory:
            path = Path(directory)
            old = os.environ.pop("ADTY_SMOKE_BASE_URL", None)
            try:
                runtime = self.smoke.Runtime(path, path)
                rows = self.smoke.run_steps([
                    ("S-01", lambda: runtime.command("S-01", [sys.executable, "-c", "print('tool contract')"])),
                    ("S-05", runtime.postgres_ready),
                ], path)
            finally:
                if old is not None:
                    os.environ["ADTY_SMOKE_BASE_URL"] = old
        self.assertEqual(["passed", "failed"], [row["outcome"] for row in rows])

    def test_public_or_embedded_credential_service_url_is_rejected(self):
        for address in ("https://example.com", "http://user:pass@localhost:8080", "file:///tmp/service"):
            with self.subTest(address=address), self.assertRaises(self.smoke.SmokeFailure):
                self.smoke.local_origin(address)
        self.assertEqual("http://localhost:8080", self.smoke.local_origin("http://localhost:8080/"))

    def test_failed_step_is_preserved_and_later_steps_are_unexecuted(self):
        calls = []

        def passed():
            calls.append("first")
            return {"exit_code": 0}

        def failed():
            calls.append("second")
            raise self.smoke.SmokeFailure("受控工具断言失败")

        with tempfile.TemporaryDirectory(prefix="adty-smoke-contract-") as directory:
            result = self.smoke.run_steps([("S-01", passed), ("S-02", failed), ("S-03", passed)], Path(directory))
        self.assertEqual(["first", "second"], calls)
        self.assertEqual(["passed", "failed", "unexecuted"], [row["outcome"] for row in result])
        self.assertEqual([0, 1, None], [row["exit_code"] for row in result])

    def test_nonzero_process_exit_is_not_turned_into_success(self):
        with tempfile.TemporaryDirectory(prefix="adty-smoke-contract-") as directory:
            result = self.smoke.run_steps([("S-01", lambda: {"exit_code": 4})], Path(directory))
        self.assertEqual("failed", result[0]["outcome"])
        self.assertEqual(4, result[0]["exit_code"])

    def test_health_step_targets_api_health_endpoint(self):
        with tempfile.TemporaryDirectory(prefix="adty-smoke-contract-") as directory:
            runtime = self.smoke.Runtime(Path(directory), Path(directory))
            seen = []
            runtime.api = lambda method, path, data=None, expect_success=True: seen.append((method, path, expect_success))
            runtime.health()
        self.assertEqual([("GET", "/api/health", True)], seen)

    def test_browser_business_requires_postgresql_18(self):
        script = (Path(__file__).resolve().parents[1] / "browser-business.sh").read_text(encoding="utf-8")
        self.assertIn("adty_pg_version / 10000", script)
        self.assertIn("-ne 18", script)


if __name__ == "__main__":
    unittest.main(verbosity=2)
