export type Middleware = (ctx: WindforceContext, next: () => Promise<unknown>) => Promise<unknown>

/** The server-signed approve/reject URLs minted for an upcoming approval (ctx.approval). */
export interface ResumeUrls {
  approve: string
  reject: string
  resume_id: number
  step_index: number
  expires_at: number
}

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
  approval: { getResumeUrls(approver?: string): Promise<ResumeUrls> }
  flow: { resumeValue?: unknown }
}

export interface AppConfig {
  actions: Record<string, (ctx: WindforceContext) => unknown | Promise<unknown>>
  use?: Middleware[]
  onError?: (ctx: WindforceContext, err: unknown) => unknown | Promise<unknown>
}

export declare function createApp(config: AppConfig): (ctx: WindforceContext) => Promise<unknown>
