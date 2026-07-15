from __future__ import annotations

from dataclasses import dataclass
import json
import time
from typing import Any, Mapping, Sequence
from urllib.error import HTTPError, URLError
from urllib.parse import quote
from urllib.request import Request, urlopen


TERMINAL_STATES = frozenset({"SUCCEEDED", "FAILED", "CANCELED", "EXPIRED"})


class WindforceAPIError(RuntimeError):
    def __init__(self, status: int, code: str, message: str, body: Any = None) -> None:
        super().__init__(message)
        self.status = status
        self.code = code
        self.message = message
        self.body = body


class WindforceTransportError(RuntimeError):
    pass


class WindforceTimeoutError(TimeoutError):
    def __init__(self, run_id: str, state: str, timeout_seconds: float) -> None:
        super().__init__(f"run {run_id} did not settle within {timeout_seconds:g}s (state={state})")
        self.run_id = run_id
        self.state = state
        self.timeout_seconds = timeout_seconds


@dataclass(frozen=True)
class Run:
    run_id: str
    state: str
    app: str
    action: str
    job_id: str = ""
    correlation_id: str = ""
    replayed: bool = False
    pinned_release: Mapping[str, Any] | None = None
    raw: Mapping[str, Any] | None = None

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "Run":
        return cls(
            run_id=str(value.get("run_id") or ""),
            job_id=str(value.get("job_id") or ""),
            state=str(value.get("state") or "").upper(),
            app=str(value.get("app") or ""),
            action=str(value.get("action") or ""),
            correlation_id=str(value.get("correlation_id") or ""),
            replayed=bool(value.get("replayed")),
            pinned_release=value.get("pinned_release") if isinstance(value.get("pinned_release"), Mapping) else None,
            raw=dict(value),
        )


class WindforceExecutionClient:
    def __init__(
        self,
        base_url: str,
        *,
        workspace: str = "default",
        token: str = "",
        request_timeout_seconds: float = 10,
        poll_interval_seconds: float = 0.1,
    ) -> None:
        self.base_url = str(base_url).rstrip("/")
        self.workspace = str(workspace).strip() or "default"
        self.token = str(token).strip()
        self.request_timeout_seconds = max(0.1, float(request_timeout_seconds))
        self.poll_interval_seconds = max(0.01, float(poll_interval_seconds))

    def create_run(
        self,
        *,
        app: str,
        action: str,
        input: Any,
        adapter: str = "",
        trigger_kind: str = "",
        trigger_headers: Mapping[str, Any] | None = None,
        correlation_id: str = "",
        idempotency_key: str = "",
        env: Sequence[str] = (),
    ) -> Run:
        payload: dict[str, Any] = {
            "app": str(app),
            "action": str(action),
            "input": input,
        }
        optional = {
            "adapter": adapter,
            "trigger_kind": trigger_kind,
            "correlation_id": correlation_id,
            "idempotency_key": idempotency_key,
        }
        payload.update({key: value for key, value in optional.items() if str(value).strip()})
        if trigger_headers:
            payload["trigger_headers"] = dict(trigger_headers)
        if env:
            payload["env"] = [str(value) for value in env]
        response = self._request("POST", self._workspace_path("runs"), payload)
        return Run.from_dict(response)

    def get_run(self, run_id: str) -> Run:
        response = self._request("GET", self._workspace_path("runs", run_id))
        return Run.from_dict(response)

    def wait(self, run_id: str, *, timeout_seconds: float) -> Run:
        timeout_seconds = max(0.0, float(timeout_seconds))
        deadline = time.monotonic() + timeout_seconds
        last = self.get_run(run_id)
        while last.state not in TERMINAL_STATES:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise WindforceTimeoutError(run_id, last.state, timeout_seconds)
            time.sleep(min(self.poll_interval_seconds, remaining))
            last = self.get_run(run_id)
        return last

    def get_result(self, run_id: str) -> Mapping[str, Any]:
        return self._request("GET", self._workspace_path("runs", run_id, "result"), accepted_statuses=(200, 202))

    def cancel(self, run_id: str, *, reason: str = "") -> Run:
        response = self._request("POST", self._workspace_path("runs", run_id, "cancel"), {"reason": reason})
        return Run.from_dict(response)

    def describe_app(self, app: str) -> Mapping[str, Any]:
        return self._request("GET", self._workspace_path("apps", app))

    def _workspace_path(self, *segments: str) -> str:
        encoded = [quote(self.workspace, safe=""), *(quote(str(value), safe="") for value in segments)]
        return "/execution/v1/workspaces/" + "/".join(encoded)

    def _request(
        self,
        method: str,
        path: str,
        payload: Any = None,
        *,
        accepted_statuses: Sequence[int] = (200, 201),
    ) -> Mapping[str, Any]:
        body = None
        headers = {"Accept": "application/json"}
        if payload is not None:
            body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
            headers["Content-Type"] = "application/json; charset=utf-8"
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        request = Request(self.base_url + path, data=body, headers=headers, method=method)
        try:
            with urlopen(request, timeout=self.request_timeout_seconds) as response:
                status = int(response.status)
                value = _decode_response(response.read())
        except HTTPError as exc:
            value = _decode_response(exc.read())
            code, message = _api_error(value, str(exc.reason))
            raise WindforceAPIError(int(exc.code), code, message, value) from exc
        except (URLError, OSError, TimeoutError) as exc:
            raise WindforceTransportError(str(exc)) from exc
        if status not in accepted_statuses:
            code, message = _api_error(value, f"unexpected HTTP status {status}")
            raise WindforceAPIError(status, code, message, value)
        if not isinstance(value, Mapping):
            raise WindforceTransportError("Windforce response is not a JSON object")
        return value


def _decode_response(data: bytes) -> Any:
    if not data:
        return {}
    try:
        return json.loads(data.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return {"error": {"code": "invalid_response", "message": data.decode("utf-8", errors="replace")}}


def _api_error(value: Any, fallback: str) -> tuple[str, str]:
    if isinstance(value, Mapping):
        error = value.get("error")
        if isinstance(error, Mapping):
            return str(error.get("code") or "api_error"), str(error.get("message") or fallback)
        if error:
            return "api_error", str(error)
    return "api_error", fallback
