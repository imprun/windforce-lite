#!/usr/bin/env python3
"""Small CLI for the windforce-lite control-plane API.

The server owns the contract. This tool only calls the `/api/w/{workspace}/...`
control-plane endpoints so local development follows the real flow:
validate and register git source -> deploy current commit -> inspect materialized schemas.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
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
    parser.add_argument(
        "--actor",
        default=os.environ.get("WINDFORCE_LITE_ACTOR", ""),
        help="optional actor subject sent as X-Windforce-Actor",
    )
    parser.add_argument("--pretty", action="store_true", help="pretty-print JSON output")

    sub = parser.add_subparsers(dest="command", required=True)

    register = sub.add_parser("register", help="register a git source")
    register.add_argument("--name", required=True)
    register.add_argument("--repo-url", "--repo", dest="repo_url", required=True)
    register.add_argument("--branch", default="main")
    register.add_argument("--subpath", default="")
    register.add_argument("--creds-ref", dest="creds_ref", default="")
    add_git_auth_args(register)
    register.set_defaults(func=cmd_register)

    probe = sub.add_parser("probe", help="probe a remote git source")
    probe.add_argument("--repo-url", "--repo", dest="repo_url", required=True)
    probe.add_argument("--branch", default="main")
    probe.add_argument("--creds-ref", dest="creds_ref", default="")
    add_git_auth_args(probe)
    probe.set_defaults(func=cmd_probe)

    list_sources = sub.add_parser("git-sources", help="list registered git sources")
    list_sources.set_defaults(func=cmd_git_sources)

    sync = sub.add_parser("sync", help="sync a registered git source by returned numeric id")
    sync.add_argument(
        "--git-source-id",
        dest="git_source_id",
        required=True,
        help="numeric git source id returned by register/list",
    )
    sync.set_defaults(func=cmd_sync)

    deploy = sub.add_parser("deploy", help="deploy the current commit of a registered git source")
    deploy.add_argument(
        "--git-source-id",
        dest="git_source_id",
        required=True,
        help="numeric git source id returned by register/list",
    )
    deploy.set_defaults(func=cmd_deploy)

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

    schema = sub.add_parser(
        "schema",
        help="get action input/output schemas via the control-plane schema endpoint",
    )
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

    control_openapi = sub.add_parser("control-openapi", help="get workspace control-plane OpenAPI")
    control_openapi.set_defaults(func=cmd_control_openapi)

    run = sub.add_parser("run", help="enqueue an action job")
    run.add_argument("--app", required=True)
    run.add_argument("--action", required=True)
    add_json_input_args(run)
    run.set_defaults(func=cmd_run)

    run_wait = sub.add_parser("run-wait", help="run an action and wait for a terminal result")
    run_wait.add_argument("--app", required=True)
    run_wait.add_argument("--action", required=True)
    run_wait.add_argument("--timeout-ms", type=int, default=None)
    add_json_input_args(run_wait)
    run_wait.set_defaults(func=cmd_run_wait)

    webhook = sub.add_parser("webhook", help="invoke an action through the webhook route")
    webhook.add_argument("--app", required=True)
    webhook.add_argument("--action", required=True)
    add_json_input_args(webhook)
    webhook.set_defaults(func=cmd_webhook)

    jobs = sub.add_parser("jobs", help="list jobs")
    jobs.add_argument("--status", default="")
    jobs.add_argument("--app", default="")
    jobs.add_argument("--action", default="")
    jobs.add_argument("--trigger-kind", dest="trigger_kind", default="")
    jobs.add_argument("--limit", type=int, default=None)
    jobs.add_argument("--cursor", default="")
    jobs.add_argument("--since", default="")
    jobs.add_argument("--until", default="")
    jobs.set_defaults(func=cmd_jobs)

    job = sub.add_parser("job", help="get one job status")
    job.add_argument("--job-id", required=True)
    job.set_defaults(func=cmd_job)

    job_result = sub.add_parser("job-result", help="get or poll one job result")
    job_result.add_argument("--job-id", required=True)
    job_result.set_defaults(func=cmd_job_result)

    job_logs = sub.add_parser("job-logs", help="get one job log stream")
    job_logs.add_argument("--job-id", required=True)
    job_logs.add_argument("--tail-bytes", type=int, default=None)
    job_logs.set_defaults(func=cmd_job_logs)

    job_cancel = sub.add_parser("job-cancel", help="cancel one queued or running job")
    job_cancel.add_argument("--job-id", required=True)
    job_cancel.add_argument("--reason", default="")
    job_cancel.set_defaults(func=cmd_job_cancel)

    variables = sub.add_parser("variables", help="list workspace variables")
    variables.set_defaults(func=cmd_variables)

    variable_set = sub.add_parser("variable-set", help="set a workspace or app-scoped variable")
    variable_set.add_argument("--path", required=True)
    value_group = variable_set.add_mutually_exclusive_group(required=True)
    value_group.add_argument("--value")
    value_group.add_argument("--value-env", dest="value_env")
    variable_set.add_argument("--app", dest="app_key", default="")
    variable_set.add_argument("--secret", action="store_true", help="store as a secret variable")
    variable_set.add_argument("--description", default="")
    variable_set.set_defaults(func=cmd_variable_set)

    variable_get = sub.add_parser("variable-get", help="get one variable by path")
    variable_get.add_argument("--path", required=True)
    variable_get.add_argument("--app", dest="app_key", default="")
    variable_get.set_defaults(func=cmd_variable_get)

    variable_delete = sub.add_parser("variable-delete", help="delete one variable by path")
    variable_delete.add_argument("--path", required=True)
    variable_delete.add_argument("--app", dest="app_key", default="")
    variable_delete.set_defaults(func=cmd_variable_delete)

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
    if isinstance(payload, RawOutput):
        sys.stdout.write(str(payload))
        return 0
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


class RawOutput(str):
    pass


def add_json_input_args(parser: argparse.ArgumentParser) -> None:
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--input", dest="input_json", help="JSON request body (default: {})")
    group.add_argument("--input-file", help="file containing the JSON request body, or '-' for stdin")


def parse_git_auth_method(value: str) -> str:
    value = value.strip()
    if value not in ("", "none", "pat", "basic"):
        raise argparse.ArgumentTypeError("expected one of: none, pat, basic")
    return value


def add_git_auth_args(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--git-auth-method",
        metavar="{none,pat,basic}",
        type=parse_git_auth_method,
        default="",
        help="git credential mode for register/probe; inferred when omitted",
    )
    parser.add_argument("--git-access-token", default="", help="PAT for the git remote")
    parser.add_argument(
        "--git-access-token-env",
        default="",
        help="env var containing a PAT for the git remote",
    )
    parser.add_argument("--git-username", default="", help="username for basic git auth")
    parser.add_argument("--git-password", default="", help="password or token for basic git auth")
    parser.add_argument(
        "--git-password-env",
        default="",
        help="env var containing the password/token for basic git auth",
    )


def git_auth_payload(args: argparse.Namespace) -> dict[str, str]:
    token = args.git_access_token or (os.environ.get(args.git_access_token_env, "") if args.git_access_token_env else "")
    password = args.git_password or (os.environ.get(args.git_password_env, "") if args.git_password_env else "")
    method = args.git_auth_method
    if not method:
        if args.git_username or password:
            method = "basic"
        elif token:
            method = "pat"
        else:
            return {}
    payload = {"auth_method": method}
    if method == "pat":
        payload["access_token"] = token
    elif method == "basic":
        payload["username"] = args.git_username
        payload["password"] = password
    return compact(payload)


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
                **git_auth_payload(args),
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
                **git_auth_payload(args),
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


def cmd_deploy(args: argparse.Namespace) -> Any:
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/git_sources/{quote_path(args.git_source_id)}/deploy",
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
    schema = get_schema(args)
    if args.field == "input":
        return schema.get("input_schema") or {}
    if args.field == "output":
        return schema.get("output_schema") or {}
    return schema


def cmd_openapi(args: argparse.Namespace) -> Any:
    return request(
        args,
        "GET",
        f"/api/w/{quote_path(args.workspace)}/apps/{quote_path(args.app)}/openapi.json",
    )


def cmd_control_openapi(args: argparse.Namespace) -> Any:
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/openapi.json")


def cmd_run(args: argparse.Namespace) -> Any:
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/jobs/run/{quote_path(args.app)}/{quote_path(args.action)}",
        load_json_body(args),
    )


def cmd_run_wait(args: argparse.Namespace) -> Any:
    query = query_string({"timeout_ms": args.timeout_ms})
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/jobs/run/{quote_path(args.app)}/{quote_path(args.action)}/wait{query}",
        load_json_body(args),
    )


def cmd_webhook(args: argparse.Namespace) -> Any:
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/jobs/webhook/{quote_path(args.app)}/{quote_path(args.action)}",
        load_json_body(args),
    )


def cmd_jobs(args: argparse.Namespace) -> Any:
    query = query_string(
        {
            "status": args.status,
            "app": args.app,
            "action": args.action,
            "trigger_kind": args.trigger_kind,
            "limit": args.limit,
            "cursor": args.cursor,
            "since": args.since,
            "until": args.until,
        }
    )
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/jobs{query}")


def cmd_job(args: argparse.Namespace) -> Any:
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/jobs/{quote_path(args.job_id)}")


def cmd_job_result(args: argparse.Namespace) -> Any:
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/jobs/{quote_path(args.job_id)}/result")


def cmd_job_logs(args: argparse.Namespace) -> Any:
    query = query_string({"tail_bytes": args.tail_bytes})
    return request_raw(args, "GET", f"/api/w/{quote_path(args.workspace)}/jobs/{quote_path(args.job_id)}/logs{query}")


def cmd_job_cancel(args: argparse.Namespace) -> Any:
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/jobs/{quote_path(args.job_id)}/cancel",
        compact({"reason": args.reason}),
    )


def cmd_variables(args: argparse.Namespace) -> Any:
    return request(args, "GET", f"/api/w/{quote_path(args.workspace)}/variables")


def cmd_variable_set(args: argparse.Namespace) -> Any:
    value = args.value
    if args.value_env:
        if args.value_env not in os.environ:
            raise APIError({"error": f"environment variable {args.value_env} is not set"})
        value = os.environ[args.value_env]
    return request(
        args,
        "POST",
        f"/api/w/{quote_path(args.workspace)}/variables",
        compact(
            {
                "path": args.path,
                "value": value,
                "app_key": args.app_key,
                "is_secret": args.secret,
                "description": args.description,
            }
        ),
    )


def cmd_variable_get(args: argparse.Namespace) -> Any:
    suffix = f"?app={quote_query(args.app_key)}" if args.app_key else ""
    return request(
        args,
        "GET",
        f"/api/w/{quote_path(args.workspace)}/variables/get/p/{quote_path_tail(args.path)}{suffix}",
    )


def cmd_variable_delete(args: argparse.Namespace) -> Any:
    suffix = f"?app={quote_query(args.app_key)}" if args.app_key else ""
    return request(
        args,
        "DELETE",
        f"/api/w/{quote_path(args.workspace)}/variables/p/{quote_path_tail(args.path)}{suffix}",
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


def get_schema(args: argparse.Namespace) -> dict[str, Any]:
    payload = request(
        args,
        "GET",
        f"/api/w/{quote_path(args.workspace)}/apps/{quote_path(args.app)}/actions/{quote_path(args.action)}/schema",
    )
    if not isinstance(payload, dict):
        raise APIError({"error": "schema response was not a JSON object"})
    return payload


def request(args: argparse.Namespace, method: str, path: str, body: Any | None = None) -> Any:
    url = args.api_url.rstrip("/") + path
    data = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")
    headers = request_headers(args, body is not None)
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


def request_raw(args: argparse.Namespace, method: str, path: str) -> RawOutput:
    url = args.api_url.rstrip("/") + path
    req = urllib.request.Request(url, headers=request_headers(args, False), method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            return RawOutput(response.read().decode("utf-8", errors="replace"))
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


def request_headers(args: argparse.Namespace, has_json_body: bool) -> dict[str, str]:
    headers = {"Accept": "application/json"}
    if has_json_body:
        headers["Content-Type"] = "application/json"
    token = os.environ.get(args.auth_token_env, "").strip()
    if token:
        headers["Authorization"] = f"Bearer {token}"
    actor = str(getattr(args, "actor", "") or "").strip()
    if actor:
        headers["X-Windforce-Actor"] = actor
    return headers


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


def query_string(params: dict[str, Any]) -> str:
    compacted = compact(params)
    if not compacted:
        return ""
    return "?" + urllib.parse.urlencode(compacted)


def load_json_body(args: argparse.Namespace) -> Any:
    if getattr(args, "input_file", None):
        if args.input_file == "-":
            raw = sys.stdin.read()
        else:
            with open(args.input_file, "r", encoding="utf-8") as stream:
                raw = stream.read()
    else:
        raw = getattr(args, "input_json", None) or "{}"
    try:
        return json.loads(raw)
    except json.JSONDecodeError as exc:
        raise APIError({"error": f"invalid JSON input: {exc.msg}"}) from exc


def quote_path(value: str) -> str:
    from urllib.parse import quote

    return quote(str(value).strip(), safe="")


def quote_path_tail(value: str) -> str:
    from urllib.parse import quote

    return "/".join(quote(part, safe="") for part in str(value).strip("/").split("/") if part)


def quote_query(value: str) -> str:
    from urllib.parse import quote

    return quote(str(value), safe="")


if __name__ == "__main__":
    raise SystemExit(main())
