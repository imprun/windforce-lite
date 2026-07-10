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
  actions: Record<string, (ctx: WindforceContext) => unknown | Promise<unknown>>
  use?: Middleware[]
  onError?: (ctx: WindforceContext, err: unknown) => unknown | Promise<unknown>
}

export declare function createApp(config: AppConfig): (ctx: WindforceContext) => Promise<unknown>
