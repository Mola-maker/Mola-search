#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Contract test for the Python client against a fake web API.

The fake asserts the paths, bodies and sign-in cookie the real
`internal/gui/detective_api.go` and `rag_api.go` routes expect. Run with:

    python -m unittest sdk/python/test_sdk.py
"""

from __future__ import annotations

import json
import os
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(__file__))
from molasearch import MolaSearchClient, MolaSearchError  # noqa: E402


class _State:
    requests: list[dict] = []


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):  # silence the test's stderr
        pass

    def _record(self, body: bytes) -> None:
        _State.requests.append(
            {
                "method": self.command,
                "path": self.path,
                "cookie": self.headers.get("Cookie", ""),
                "body": json.loads(body.decode("utf-8")) if body else None,
            }
        )

    def _reply(self, payload: dict, status: int = 200) -> None:
        data = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self) -> None:
        self._record(b"")
        if self.path == "/api/detective/modes":
            self._reply({"count": 1, "modes": [{"id": "M01"}]})
        elif self.path == "/api/rag/habits":
            self._reply({"habits": []})
        else:
            self._reply({"error": "not found"}, 404)

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length)
        self._record(body)
        if self.path == "/api/detective/investigate":
            self._reply({"verdict": "ABSTAIN", "originAgent": "Detective UI", "citations": []})
        elif self.path == "/api/rag/hotswap":
            self._reply({"activeDbId": "markdown_vault", "swap": {"sha256": "abc"}})
        elif self.path == "/api/rag/search":
            self._reply({"hits": [], "tookMs": 1})
        else:
            self._reply({"error": "not found"}, 404)


class ClientContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        cls.port = cls.server.server_address[1]
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls) -> None:
        cls.server.shutdown()
        cls.server.server_close()

    def setUp(self) -> None:
        _State.requests.clear()

    def test_cookie_is_named_after_the_web_port(self) -> None:
        c = MolaSearchClient(f"http://127.0.0.1:{self.port}", web_key="secret-key")
        c.modes()
        self.assertEqual(_State.requests[0]["cookie"], f"magpie_web_{self.port}=secret-key")

    def test_modes_reads_the_catalog_route(self) -> None:
        c = MolaSearchClient(f"http://127.0.0.1:{self.port}")
        self.assertEqual(c.modes(), [{"id": "M01"}])
        self.assertEqual(_State.requests[0]["path"], "/api/detective/modes")

    def test_investigate_sends_only_real_fields(self) -> None:
        c = MolaSearchClient(f"http://127.0.0.1:{self.port}")
        inv = c.investigate("北京天气", mode_id="M01", debate=True)
        self.assertEqual(inv["verdict"], "ABSTAIN")
        req = _State.requests[0]
        self.assertEqual(req["path"], "/api/detective/investigate")
        self.assertEqual(
            req["body"],
            {"query": "北京天气", "modeId": "M01", "debate": True, "enableCU": False},
        )
        self.assertNotIn("audience", req["body"])
        self.assertNotIn("tokenBudget", req["body"])

    def test_hotswap_uses_the_real_target_key(self) -> None:
        c = MolaSearchClient(f"http://127.0.0.1:{self.port}")
        c.rag_hotswap("markdown_vault", memory_mode="persistent")
        self.assertEqual(_State.requests[0]["path"], "/api/rag/hotswap")
        self.assertEqual(
            _State.requests[0]["body"],
            {"targetDbId": "markdown_vault", "memoryMode": "persistent"},
        )

    def test_http_errors_keep_status_and_body(self) -> None:
        c = MolaSearchClient(f"http://127.0.0.1:{self.port}")
        with self.assertRaises(MolaSearchError) as ctx:
            c._request("/api/not-a-route")
        self.assertEqual(ctx.exception.status, 404)
        self.assertIn("not found", ctx.exception.body)


if __name__ == "__main__":
    unittest.main()
