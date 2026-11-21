import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("publish-release.sh").resolve()


class PublishReleaseTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.log = self.root / "requests"
        self.dist = self.root / "dist"
        self.dist.mkdir()
        mock_bin = self.root / "bin"
        mock_bin.mkdir()
        curl = mock_bin / "curl"
        curl.write_text("""#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
with open(os.environ['REQUEST_LOG'], 'a') as out:
    out.write(json.dumps(args) + '\\n')
if os.environ.get('FAIL_UPLOAD') and '-F' in args:
    sys.exit(22)
print('{"id":123}')
""")
        curl.chmod(0o755)
        self.env = dict(os.environ, PATH=f"{mock_bin}:{os.environ['PATH']}",
                        REQUEST_LOG=str(self.log), RELEASE_TOKEN="test-token",
                        GITHUB_SHA="a" * 40, GITHUB_REF="refs/heads/trim",
                        GITHUB_EVENT_NAME="push", GITHUB_RUN_ID="42",
                        GITHUB_RUN_ATTEMPT="1", GITHUB_STEP_SUMMARY="",
                        GITHUB_SERVER_URL="https://gitea.example.test",
                        GITHUB_REPOSITORY="BakeLab/gitea")
        binary = b"test binary"
        (self.dist / "gitea-linux-amd64").write_bytes(binary)
        (self.dist / "gitea-linux-amd64.sha256").write_text(
            f"{hashlib.sha256(binary).hexdigest()}  gitea-linux-amd64\n")
        (self.dist / "manifest.json").write_text(json.dumps({
            "commit": "a" * 40, "platform": "linux-amd64", "version": "Gitea test"}))

    def publish(self, *args):
        return subprocess.run(["bash", str(SCRIPT), str(self.dist), *args],
                              env=self.env, capture_output=True, text=True)

    def requests(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def test_only_complete_release_is_public(self):
        result = self.publish()
        self.assertEqual(result.returncode, 0, result.stderr)
        requests = self.requests()
        self.assertEqual(len(requests), 5)
        payload = json.loads(requests[0][requests[0].index("--data") + 1])
        self.assertTrue(payload["draft"])
        self.assertEqual(payload["target_commitish"], "a" * 40)
        self.assertEqual(payload["tag_name"], "trim-aaaaaaaaaaaa-42-1")
        self.assertEqual(sum("-F" in request for request in requests), 3)
        self.assertIn("PATCH", requests[-1])
        self.assertIn('{"draft":false}', requests[-1])

    def test_branch_validation_never_publishes(self):
        self.env["GITHUB_REF"] = "refs/heads/main"
        self.env.pop("RELEASE_TOKEN")
        self.assertEqual(self.publish("--check").returncode, 0)
        self.assertNotEqual(self.publish().returncode, 0)
        self.assertFalse(self.log.exists())
        self.env["GITHUB_REF"] = "refs/heads/trim"
        self.env["GITHUB_EVENT_NAME"] = "pull_request"
        self.assertNotEqual(self.publish().returncode, 0)
        self.assertFalse(self.log.exists())

    def test_invalid_assets_never_publish(self):
        self.env["GITHUB_SHA"] = "b" * 40
        self.assertNotEqual(self.publish().returncode, 0)
        self.env["GITHUB_SHA"] = "a" * 40
        (self.dist / "gitea-linux-amd64").write_text("corrupted")
        self.assertNotEqual(self.publish().returncode, 0)
        (self.dist / "gitea-linux-amd64").unlink()
        self.assertNotEqual(self.publish().returncode, 0)
        self.assertFalse(self.log.exists())

    def test_failed_upload_leaves_draft(self):
        self.env["FAIL_UPLOAD"] = "1"
        self.assertNotEqual(self.publish().returncode, 0)
        self.assertFalse(any("PATCH" in request for request in self.requests()))


if __name__ == "__main__":
    unittest.main()
