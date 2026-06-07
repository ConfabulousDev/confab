declare module "@opencode-ai/plugin" {
  export type Plugin = (
    context: PluginContext,
  ) => PluginHooks | Promise<PluginHooks>

  // Session events per https://opencode.ai/docs/plugins/
  export type Event =
    | { type: "session.created"; properties: { info: { id: string; directory: string } } }
    | { type: "session.compacted"; properties: { sessionID: string } }
    | { type: "session.deleted"; properties: { sessionID: string } }
    | { type: "session.diff"; properties: { sessionID: string } }
    | { type: "session.error"; properties: { sessionID: string; error: string } }
    | { type: "session.idle"; properties: { sessionID: string } }
    | { type: "session.status"; properties: { sessionID: string; status: string } }
    | { type: "session.updated"; properties: { sessionID: string } }
    // Message events
    | { type: "message.updated"; properties: { sessionID: string; messageID: string } }
    | { type: "message.removed"; properties: { sessionID: string; messageID: string } }
    | { type: "message.part.updated"; properties: { sessionID: string; messageID: string } }
    | { type: "message.part.removed"; properties: { sessionID: string; messageID: string } }
    // Tool events
    | { type: "tool.execute.before"; properties: { tool: string; input: any } }
    | { type: "tool.execute.after"; properties: { tool: string; output: any } }
    // File events
    | { type: "file.edited"; properties: { path: string } }
    // Permission events
    | { type: "permission.asked"; properties: { permission: string } }
    | { type: "permission.replied"; properties: { permission: string; granted: boolean } }
    // Other events (not exhaustive)
    | { type: string; properties: Record<string, any> }

  interface ShellOutput {
    stdout: string
    exitCode: number
  }

  interface ShellPromise extends Promise<ShellOutput> {
    quiet(): this
  }

  interface PluginContext {
    $: (
      template: TemplateStringsArray,
      ...values: any[]
    ) => ShellPromise
    serverUrl: URL
    project?: { id: string; name?: string }
    directory?: string
    worktree?: string
    client?: any
  }

  // CF-549 M1: the plugin reads `process.pid` to send opencode's PID
  // authoritatively to confab. Declared locally to avoid pulling in the
  // full @types/node devDependency.
  global {
    var process: { pid: number }
  }

  interface PluginHooks {
    event?: (input: { event: Event }) => Promise<void>
    dispose?: () => Promise<void>
    // Tool hooks
    "tool.execute.before"?: (input: { tool: string; input: any }, output: { input: any }) => Promise<void> | void
    "tool.execute.after"?: (input: { tool: string; output: any }, output: any) => Promise<void> | void
    // Shell hooks
    "shell.env"?: (input: { cwd: string }, output: { env: Record<string, string> }) => Promise<void> | void
    // Experimental hooks
    "experimental.session.compacting"?: (input: any, output: { context: string[]; prompt?: string }) => Promise<void> | void
  }
}
