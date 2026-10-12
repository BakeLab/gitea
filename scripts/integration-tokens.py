#!/usr/bin/env python3
"""Create private PAT fixtures on the disposable CI Gitea instance."""

import base64
import json
import os
import urllib.request
import uuid
from pathlib import Path


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def main():
    url = os.environ["GITEA_CLI_TEST_URL"].rstrip("/")
    fixtures = [
        ("integration", "gt-local-integration-pass", "GITEA_CLI_TEST_TOKEN_FILE"),
        ("gt-reviewer", "gt-local-reviewer-pass", "GITEA_CLI_TEST_REVIEWER_TOKEN_FILE"),
    ]
    # Do not follow redirects with Basic credentials.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    for user, password, variable in fixtures:
        request = urllib.request.Request(
            f"{url}/api/v1/users/{user}/tokens",
            data=json.dumps({"name": f"gt-ci-{uuid.uuid4().hex}", "scopes": ["all"]}).encode(),
            headers={
                "Authorization": "Basic " + base64.b64encode(f"{user}:{password}".encode()).decode(),
                "Content-Type": "application/json",
            },
            method="POST",
        )
        with opener.open(request, timeout=30) as response:
            token = json.load(response)["sha1"]
        destination = Path(os.environ[variable])
        destination.parent.mkdir(parents=True, exist_ok=True)
        descriptor = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as output:
            output.write(token)


if __name__ == "__main__":
    main()
