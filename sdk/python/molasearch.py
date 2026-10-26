#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""MolaSearch client for the real `magpie web` API.

MolaSearch runs inside a magpie build. Two ports matter, and they are not
interchangeable:

* ``3425`` is magpie's LLM gateway (OpenAI/Anthropic/Gemini compatible).
  It is not this API.
* ``3430`` is ``magpie web``: the Detective page and its ``/api/*`` routes.
  This client talks to that one.

Start the server with ``molasearch.exe web --addr 127.0.0.1:3430``. If you
set ``MAGPIE_WEB_KEY``, pass the same value as ``web_key``: every request
then carries the ``magpie_web_<port>`` sign-in cookie the page would hold.
Without a key, the API is open only on loopback, as `magpie web` is.

Only the standard library is used. Every method calls a route that exists in
``internal/gui/detective_api.go`` or ``internal/gui/rag_api.go``; there is no
client-side fallback data.
"""

from __future__ import annotations

import base64
import json
import urllib.error
import urllib.request
from typing import Any, Dict, Iterable, List, Optional
from urllib.parse import urlsplit

DEFAULT_BASE_URL = "http://127.0.0.1:3430"


class MolaSearchError(RuntimeError):
    """An HTTP error from the web API, with the status and body kept."""

    def __init__(self, status: int, body: str) -> None:
        super().__init__(f"MolaSearch API error {status}: {body}")
        self.status = status
        self.body = body


class MolaSearchClient:
    """Client for the Detective and RAG routes of a running `magpie web`."""

    def __init__(
        self,
        base_url: str = DEFAULT_BASE_URL,
        web_key: Optional[str] = None,
        timeout: float = 120.0,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.web_key = web_key
        self.timeout = timeout

    # -- transport ---------------------------------------------------------

    def _headers(self) -> Dict[str, str]:
        headers = {
            "Content-Type": "application/json; charset=utf-8",
            "Accept": "application/json",
        }
        if self.web_key:
            # `magpie web` names its sign-in cookie after the port it serves.
            parts = urlsplit(self.base_url)
            port = parts.port or (443 if parts.scheme == "https" else 80)
            headers["Cookie"] = f"magpie_web_{port}={self.web_key}"
        return headers

    def _request(
        self,
        path: str,
        method: str = "GET",
        payload: Optional[Dict[str, Any]] = None,
    ) -> Dict[str, Any]:
        data = (
            json.dumps(payload, ensure_ascii=False).encode("utf-8")
            if payload is not None
            else None
        )
        req = urllib.request.Request(
            self.base_url + path, data=data, method=method, headers=self._headers()
        )
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                body = resp.read().decode("utf-8")
        except urllib.error.HTTPError as err:
            raise MolaSearchError(err.code, err.read().decode("utf-8", "replace")) from err
        return json.loads(body) if body else {}

    # -- Detective ---------------------------------------------------------

    def modes(self) -> List[Dict[str, Any]]:
        """The 30-mode catalog the engine serves."""
        return self._request("/api/detective/modes").get("modes", [])

    def investigate(
        self,
        query: str,
        mode_id: str = "M11",
        debate: bool = False,
        enable_cu: bool = False,
        image_base64: Optional[str] = None,
        image_name: Optional[str] = None,
        page_url: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Run one live investigation and return the full Investigation.

        The answer is whatever the channels returned: ``verdict`` may be
        ``ABSTAIN`` with no citations, and ``redactAudit`` says what was
        masked or blocked before any request left the machine.
        """
        payload: Dict[str, Any] = {
            "query": query,
            "modeId": mode_id,
            "debate": debate,
            "enableCU": enable_cu,
        }
        if image_base64 is not None:
            payload["imageBase64"] = image_base64
        if image_name is not None:
            payload["imageName"] = image_name
        if page_url is not None:
            payload["pageUrl"] = page_url
        return self._request("/api/detective/investigate", "POST", payload)

    def state(self) -> Dict[str, Any]:
        """Provider health, the gates in force, and where the settings file lives."""
        return self._request("/api/detective/state")

    def key_status(self) -> Dict[str, Any]:
        """Which search/vision keys are configured, and where each is read from."""
        return self._request("/api/detective/keys")

    def upload_image(self, data: bytes, name: str = "image.jpg") -> Dict[str, Any]:
        """Fingerprint an image (dHash/aHash/EXIF) without running a search."""
        return self._request(
            "/api/detective/upload-image",
            "POST",
            {"name": name, "base64": base64.b64encode(data).decode("ascii")},
        )

    def set_channel(self, channel_id: str, enabled: bool) -> Dict[str, Any]:
        """Turn one search provider on or off; the rest of a run keeps serving."""
        return self._request(
            "/api/detective/channels", "POST", {"id": channel_id, "enabled": enabled}
        )

    def settings(self) -> Dict[str, Any]:
        """The gates in force, their defaults, and the file they live in."""
        return self._request("/api/detective/settings")

    def save_settings(self, settings: Dict[str, Any]) -> Dict[str, Any]:
        """Write the gates the engine reads. The server validates and persists them."""
        return self._request("/api/detective/settings", "POST", settings)

    # -- Stakeout ----------------------------------------------------------

    def stakeouts(self) -> List[Dict[str, Any]]:
        return self._request("/api/detective/stakeout").get("jobs", [])

    def create_stakeout(self, job: Dict[str, Any]) -> Dict[str, Any]:
        """Save a watch; the server fills id/title/interval when omitted."""
        return self._request("/api/detective/stakeout", "POST", job)

    def delete_stakeout(self, job_id: str) -> Dict[str, Any]:
        return self._request(f"/api/detective/stakeout/{job_id}", "DELETE")

    def check_stakeout(self, job_id: Optional[str] = None) -> Dict[str, Any]:
        """Re-check one watch, or every watch when ``job_id`` is omitted."""
        payload = {"jobId": job_id} if job_id else {}
        return self._request("/api/detective/stakeout/check", "POST", payload)

    def inject_agent(self, agent: str, enabled: bool = True) -> Dict[str, Any]:
        """Write (or remove) the skill and MCP entry in an agent's own folders."""
        return self._request(
            "/api/detective/modes/inject", "POST", {"agent": agent, "enabled": enabled}
        )

    # -- Memory store ------------------------------------------------------

    def rag_status(self) -> Dict[str, Any]:
        return self._request("/api/rag/status")

    def rag_search(self, query: str, top_k: int = 5) -> Dict[str, Any]:
        return self._request("/api/rag/search", "POST", {"query": query, "topK": top_k})

    def rag_insert(self, record: Dict[str, Any]) -> Dict[str, Any]:
        """Insert one CanonicalMemoryRecord; topic and evidenceText are required."""
        return self._request("/api/rag/records", "POST", record)

    def rag_hotswap(
        self,
        target_db_id: str,
        embedding_model: Optional[str] = None,
        reranker_model: Optional[str] = None,
        memory_mode: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Three-phase hot swap: backup, dual-write, re-embed, hash verify, flip.

        A verification mismatch is an error and leaves the active backend
        unchanged (the route fails closed).
        """
        payload: Dict[str, Any] = {"targetDbId": target_db_id}
        if embedding_model is not None:
            payload["embeddingModel"] = embedding_model
        if reranker_model is not None:
            payload["rerankerModel"] = reranker_model
        if memory_mode is not None:
            payload["memoryMode"] = memory_mode
        return self._request("/api/rag/hotswap", "POST", payload)

    def rag_export(self) -> Dict[str, Any]:
        """Write the corpus as USMF JSONL on the server; returns its path."""
        return self._request("/api/rag/coldswap/export", "POST", {})

    def rag_import(
        self,
        jsonl: Optional[str] = None,
        path: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Import USMF JSONL from a string, or from a path on the server."""
        payload: Dict[str, Any] = {}
        if jsonl is not None:
            payload["jsonl"] = jsonl
        if path is not None:
            payload["path"] = path
        return self._request("/api/rag/coldswap/import", "POST", payload)

    def habits(self) -> List[Dict[str, Any]]:
        return self._request("/api/rag/habits").get("habits", [])


__all__ = ["DEFAULT_BASE_URL", "MolaSearchClient", "MolaSearchError"]
