#!/usr/bin/env python3
"""Small CLI for the windforce-lite control-plane API.

The server owns the contract. This tool only calls the `/api/w/{workspace}/...`
control-plane endpoints so local development follows the real flow:
register git source -> sync -> inspect materialized schemas.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any


DEFAULT_API_URL = "http://127.0.0.1:8080"
DEFAULT_WORKSPACE = "default"


def main(argv: list[str] | None = None) -> int:
    configure_stdio()
    parser = argparse.ArgumentParser(description="windforce-lite control-plane API client")
    parser.add_argument(
        "--api-url",
        default=os.environ.get("WINDFORCE_LITE_API_URL", DEFAULT_API_URL),
        help=f"control-plane API base URL (default: {DEFAULT_API_URL})",
    )
    parser.add_argument(
        "--workspace",
        default=os.environ.get("WINDFORCE_LITE_WORKSPACE", DEFAULT_WORKSPACE),
        help=f"workspace id (default: {DEFAULT_WORKSPACE})",
    )
    parser.add_argument(
        "--auth-token-env",
        default="WINDFORCE_LITE_API_TOKEN",
        help="optional env var containing the API bearer token",
    )
    parser.add_argument("--pretty", action="store_true", help="pretty-print JSON output")

    sub = parser.add_subparsers(dest="command", required=True)

    register = sub.add_parser("register", help="register a git source")
    register.add_argument("--name", "--git-source-id", dest="name", required=True)
    register.add_argument("--repo-url", "--repo", dest="repo_url", required=True)
    register.add_argument("--branch", default="main")
    register.add_argument("--subpath", default="")
    register.add_argument("--token-env", "--creds-ref", dest="creds_ref", default="")
    register.set_defaults(func=cmd_register)

    probe = sub.add_parser("probe", help="probe a remote git source")
    probe.add_argument("--repo-url", "--repo", dest="repo_url", required=True)
    probe.add_argument("--branch", default="main")
    probe.add_argument("--token-env", "--creds-ref", dest="creds_ref", default="")
    probe.set_defaults(func=cmd_probe)

    list_sources = sub.add_parser("git-sources", help="list registered git sources")
    list_sources.set_defaults(func=cmd_git_sources)

    sync = sub.add_parser("sync", help="sync a registered git source by returned numeric id")
    sync.add_argument(
        "--git-source-id",
        "--name",
        dest="git_source_id",
        required=True,
        help="numeric git source id; source name is accepted for local compatibility",
    )
    # Compatibility with earlier local scripts. These fields belong to the
    # registered git source and are not sent to the sync endpoint.
    sync.add_argument("--subpath", default=argparse.SUPPRESS)
    sync.add_argument("--token-env", default=argparse.SUPPRESS)
    sync.set_defaults(func=cmd_sync)

    sample = sub.add_parser("sample", help="create and sync a managed sample git source")
    sample.add_argument("--app-key", "--app", dest="app_key", default="")
    sample.set_defaults(func=cmd_sample)

    apps = sub.add_parser("apps", help="list apps")
    apps.add_argument("--summary", action="store_true", help="return summary rows")
    apps.set_defaults(func=cmd_apps)

    app = sub.add_parser("app", help="get app detail including action schemas")
    app.add_argument("--app", required=True)
    app.set_defaults(func=cmd_app)

    action = sub.add_parser("action", help="get one action including schemas")
    action.add_argument("--app", required=True)
    action.add_argument("--action", required=True)
    action.set_defaults(func=cmd_action)

    schema = sub.add_parser("schema", help="get action input/output schemas")
    schema.add_argument("--app", required=True)
    schema.add_argument("--action", required=True)
    schema.add_argument(
        "--field",
        choices=("all", "input", "output"),
        default="all",
        help="schema field to print",
    )
    schema.set_defaults(func=cmd_schema)

    openapi = sub.add_parser("openapi", help="get app invocation OpenAPI generated from schemas")
    openapi.add_argument("--app", required=True)
    openapi.set_defaults(func=cmd_openapi)

    source = sub.add_parser("source", help="get materialized app source bundle")
    source.add_argument("--app", required=True)
    source.set_defaults(func=cmd_source)

    history = sub.add_parser("history", help="get app deployment history")
    history.add_argument("--app", required=True)
    history.set_defaults(func=cmd_history)

    args = parser.parse_args(argv)
    try:
        payload = args.func(args)
    except APIError as exc:
        print_json(exc.payload, args.pretty, stream=sys.stderr)
        return 1
    print_json(payload, args.pretty)
    return 0


def configure_stdio() -> None:
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is None:
            continue
        try:
            reconfigure(encoding="utf-8")
        except Exception:
            pass


class APIError(RuntimeError):
    def __init__(self, payload: Any):
        self.payload = payload
        super().__init__(str(payload))


def cmd_register(args: argparse.Namespace) -> Any:
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/git_sources",
        compact(
            {
                "name": args.name,
                "repo_url": args.repo_url,
                "branch": args.branch,
                "subpath": args.subpath,
                "creds_ref": args.creds_ref,
            }
        ),
    )


def cmd_probe(args: argparse.Namespace) -> Any:
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/git_sources/probe",
        compact(
            {
                "repo_url": args.repo_url,
                "branch": args.branch,
                "creds_ref": args.creds_ref,
            }
        ),
    )


def cmd_git_sources(args: argparse.Namespace) -> Any:
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/git_sources")


def cmd_sync(args: argparse.Namespace) -> Any:
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/git_sources/{quote_path(args.git_source_id)}/sync",
    )


def cmd_sample(args: argparse.Namespace) -> Any:
    body = {}
    if args.app_key:
        body["app_key"] = args.app_key
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/git_sources/sample",
        body,
    )


def cmd_apps(args: argparse.Namespace) -> Any:
    suffix = "?view=summary" if args.summary else ""
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/apps{suffix}")


def cmd_app(args: argparse.Namespace) -> Any:
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/apps/{quote_path(args.app)}")


def cmd_action(args: argparse.Namespace) -> Any:
    return get_action(args)


def cmd_schema(args: argparse.Namespace) -> Any:
    action = get_action(args)
    if args.field == "input":
        return action.get("input_schema") or {}
    if args.field == "output":
        return action.get("output_schema") or {}
    return {
        "app_key": action.get("app_key"),
        "action_key": action.get("action_key"),
        "input_schema": action.get("input_schema") or {},
        "output_schema": action.get("output_schema") or {},
    }


def cmd_openapi(args: argparse.Namespace) -> Any:
    return request(
        args,
        "GET",
        f"/api/w/{quote_path(args.workspace)}/apps/{quote_path(args.app)}/openapi.json",
    )


def cmd_source(args: argparse.Namespace) -> Any:
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/apps/{quote_path(args.app)}/source")


def cmd_history(args: argparse.Namespace) -> Any:
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/apps/{quote_path(args.app)}/history")


def get_action(args: argparse.Namespace) -> dict[str, Any]:
    payload = request(
        args,
        "GET",
        f"/api/w/{quote_path(args.workspace)}/apps/{quote_path(args.app)}/actions/{quote_path(args.action)}",
    )
    if not isinstance(payload, dict):
        raise APIError({"error": "action response was not a JSON object"})
    return payload


def request(args: argparse.Namespace, method: str, path: str, body: dict[str, Any] | None = None) -> Any:
    url = args.api_url.rstrip("/") + path
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    token = os.environ.get(args.auth_token_env, "").strip()
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            return decode_response(response.read())
    except urllib.error.HTTPError as exc:
        payload = decode_response(exc.read())
        if isinstance(payload, dict):
            payload.setdefault("status", exc.code)
            payload.setdefault("url", url)
        else:
            payload = {"status": exc.code, "url": url, "error": payload}
        raise APIError(payload) from exc
    except urllib.error.URLError as exc:
        raise APIError({"error": str(exc.reason), "url": url}) from exc


def decode_response(data: bytes) -> Any:
    if not data:
        return None
    try:
        return json.loads(data.decode("utf-8"))
    except json.JSONDecodeError:
        return data.decode("utf-8", errors="replace")


def print_json(payload: Any, pretty: bool, stream: Any = sys.stdout) -> None:
    if pretty:
        print(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True), file=stream)
    else:
        print(json.dumps(payload, ensure_ascii=False, separators=(",", ":")), file=stream)


def compact(payload: dict[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in payload.items() if value not in ("", None)}


def quote_path(value: str) -> str:
    from urllib.parse import quote

    return quote(str(value).strip(), safe="")


if __name__ == "__main__":
    raise SystemExit(main())
