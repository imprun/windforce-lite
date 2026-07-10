// windforce-client: the in-script AUTHOR SDK (ctx-first, ADR-0014).
//
//   import { createApp, type WindforceContext } from "windforce-client"
//
// Authors assemble their entrypoint with createApp and import types only. The
// platform wrapper builds the ctx object (input, trigger, identifiers, actor, and
// the variables/resources/state/http/logger methods) and calls main(ctx); this
// SDK turns an action map into that main and dispatches on ctx.action. The SDK
// never reads env or talks to the network — that all lives in the wrapper.

export type Middleware = (ctx: WindforceContext, next: () => Promise<unknown>) => Promise<unknown>

export interface WindforceContext {
  input: unknown
  trigger: {
    kind: "api" | "webhook" | "schedule" | "manual" | string
    raw?: unknown
    headers?: Record<string, string>
    scheduledFor?: string
  }
  app: string
  action: string
  job: { id: string; path?: string; workspace: string; tag: string }
  actor: { email: string; username: string; permissionedAs: string }
  logger: {
    info(...args: unknown[]): void
    warn(...args: unknown[]): void
    error(...args: unknown[]): void
    debug(...args: unknown[]): void
  }
  variables: { get(path: string): Promise<string> }
  resources: { get(path: string): Promise<unknown> }
  state: { get(): Promise<unknown>; set(value: unknown): Promise<void> }
  http: { fetch: typeof fetch }
}

export interface AppConfig {
  /** action_key -> handler. main dispatches on ctx.action. */
  actions: Record<string, (ctx: WindforceContext) => unknown | Promise<unknown>>
  /** optional onion middleware (outermost first). */
  use?: Middleware[]
  /** optional error handler; its return value becomes the result (or it rethrows). */
  onError?: (ctx: WindforceContext, err: unknown) => unknown | Promise<unknown>
}

/**
 * createApp turns an action map into the entrypoint's main(ctx). The handler for
 * ctx.action is resolved at the CORE of the middleware onion, so an unknown action
 * and every handler/middleware error propagate through `use` and `onError`
 * uniformly. The wrapper only ever calls `await main(ctx)`.
 */
export function createApp(config: AppConfig): (ctx: WindforceContext) => Promise<unknown> {
  const use = config.use ?? []
  const onError = config.onError
  return async function main(ctx: WindforceContext): Promise<unknown> {
    // Core: resolve + run the handler for ctx.action; unknown action -> throw.
    const core = async (): Promise<unknown> => {
      const handler = config.actions[ctx.action]
      if (typeof handler !== "function") {
        throw new Error("unknown action: " + ctx.action)
      }
      return handler(ctx)
    }
    // Wrap the core in the onion: use[0] is outermost (runs first).
    let next: () => Promise<unknown> = core
    for (let i = use.length - 1; i >= 0; i--) {
      const mw = use[i]
      const downstream = next
      next = () => mw(ctx, downstream)
    }
    if (onError) {
      try {
        return await next()
      } catch (err) {
        return await onError(ctx, err)
      }
    }
    return await next()
  }
}
