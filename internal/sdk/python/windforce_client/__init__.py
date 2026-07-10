"""windforce-client: the in-script AUTHOR SDK for Python (ctx-first, ADR-0014).

    from windforce_client import create_app, WindforceContext

Authors assemble their entrypoint with ``create_app`` and import types only. The
platform wrapper builds the ``ctx`` object (input, trigger, identifiers, actor,
and the variables/resources/state/http/logger members) and calls ``main(ctx)``;
this SDK turns an action map into that ``main`` and dispatches on ``ctx.action``.
The SDK never reads the environment or talks to the network — that all lives in
the wrapper.

This mirrors the TypeScript SDK (``sdk/typescript``) capability-for-capability;
only the surface idiom differs (snake_case, ``async def``). The distribution name
is ``windforce-client`` in every language (ADR-0038); the Python import name is
``windforce_client``.

Note: the Python *runtime adapter* (executor ``script_lang`` branch, wrapper
generator, install path, SDK injection, language image) is roadmapped — this
package is the author library/types only (ADR-0038 §5).
"""

from __future__ import annotations

import inspect
from typing import (
    Any,
    Awaitable,
    Callable,
    Mapping,
    Protocol,
    Sequence,
    TypedDict,
    runtime_checkable,
)

__all__ = [
    "create_app",
    "WindforceContext",
    "Trigger",
    "Job",
    "Actor",
    "Logger",
    "Variables",
    "Resources",
    "State",
    "Http",
    "Approval",
    "Flow",
    "ResumeUrls",
    "Handler",
    "Middleware",
    "Next",
]

# A handler may be sync or async; create_app awaits the result either way.
Handler = Callable[["WindforceContext"], Any]
# `next` advances to the downstream middleware (or the core handler).
Next = Callable[[], Awaitable[Any]]
# Onion middleware: receives ctx and the downstream `next`, returns the result.
Middleware = Callable[["WindforceContext", Next], Awaitable[Any]]


@runtime_checkable
class Logger(Protocol):
    def info(self, *args: Any) -> None: ...
    def warn(self, *args: Any) -> None: ...
    def error(self, *args: Any) -> None: ...
    def debug(self, *args: Any) -> None: ...


@runtime_checkable
class Variables(Protocol):
    async def get(self, path: str) -> str: ...


@runtime_checkable
class Resources(Protocol):
    async def get(self, path: str) -> Any: ...


@runtime_checkable
class State(Protocol):
    async def get(self) -> Any: ...
    async def set(self, value: Any) -> None: ...


@runtime_checkable
class Http(Protocol):
    # The platform's egress capability; the wrapper supplies the implementation.
    async def fetch(self, url: str, **options: Any) -> Any: ...


class ResumeUrls(TypedDict):
    """The server-signed approve/reject URLs minted for an upcoming approval."""

    approve: str
    reject: str
    resume_id: int
    step_index: int
    expires_at: int


@runtime_checkable
class Approval(Protocol):
    # Flow HITL (ADR-0053): mint the approve/reject URLs for the approval step that
    # immediately follows this one (call from the action right before an approval).
    # approver scopes the resume slot — required for a multi-approval step.
    async def get_resume_urls(self, approver: str | None = None) -> "ResumeUrls": ...


@runtime_checkable
class Flow(Protocol):
    # resume_value is the approver's submitted value, present only on the action that runs
    # immediately AFTER an approval (also delivered as ctx.input); None for other steps.
    resume_value: Any


@runtime_checkable
class Trigger(Protocol):
    kind: str  # "api" | "webhook" | "schedule" | "manual" | str
    raw: Any
    headers: Mapping[str, str] | None
    scheduled_for: str | None


@runtime_checkable
class Job(Protocol):
    id: str
    path: str | None
    workspace: str
    tag: str


@runtime_checkable
class Actor(Protocol):
    email: str
    username: str
    permissioned_as: str


@runtime_checkable
class WindforceContext(Protocol):
    """The execution context the wrapper builds and passes to ``main(ctx)``.

    Structural (like the TS ``interface``): the wrapper supplies a duck-typed
    object that satisfies this shape; the SDK never constructs it.
    """

    input: Any
    trigger: Trigger
    app: str
    action: str
    job: Job
    actor: Actor
    logger: Logger
    variables: Variables
    resources: Resources
    state: State
    http: Http
    approval: Approval
    flow: Flow


async def _maybe_await(value: Any) -> Any:
    """Await ``value`` if it is awaitable, else return it as-is.

    Handlers and ``on_error`` may be sync or async; this normalizes both.
    """
    if inspect.isawaitable(value):
        return await value
    return value


def create_app(
    *,
    actions: Mapping[str, Handler],
    use: Sequence[Middleware] | None = None,
    on_error: Callable[["WindforceContext", Exception], Any] | None = None,
) -> Callable[["WindforceContext"], Awaitable[Any]]:
    """Turn an action map into the entrypoint's ``main(ctx)``.

    The handler for ``ctx.action`` is resolved at the CORE of the middleware
    onion, so an unknown action and every handler/middleware error propagate
    through ``use`` and ``on_error`` uniformly. The wrapper only ever calls
    ``await main(ctx)``.

    Args:
        actions: ``action_key -> handler``; ``main`` dispatches on ``ctx.action``.
        use: optional onion middleware, outermost first (``use[0]`` runs first).
        on_error: optional error handler; its return value becomes the result
            (or it re-raises).
    """
    middlewares = list(use or [])

    async def main(ctx: "WindforceContext") -> Any:
        # Core: resolve + run the handler for ctx.action; unknown action -> raise.
        async def core() -> Any:
            handler = actions.get(ctx.action)
            if handler is None or not callable(handler):
                raise ValueError(f"unknown action: {ctx.action}")
            return await _maybe_await(handler(ctx))

        # Wrap the core in the onion: use[0] is outermost (runs first).
        nxt: Next = core
        for mw in reversed(middlewares):
            # Bind mw/downstream per iteration (avoid late-binding closure bug).
            def make(mw: Middleware, downstream: Next) -> Next:
                def step() -> Awaitable[Any]:
                    return mw(ctx, downstream)

                return step

            nxt = make(mw, nxt)

        if on_error is not None:
            try:
                return await nxt()
            except Exception as err:  # noqa: BLE001 — mirror TS: catch + delegate
                return await _maybe_await(on_error(ctx, err))
        return await nxt()

    return main
