import { describe, it, expect, vi, beforeEach, afterEach } from "vitest"
import { ConfabSync } from "./confab-sync"

describe("ConfabSync", () => {
  const mock$ = vi.fn() as any
  const serverUrl = new URL("http://localhost:4096")

  function mkPromise() {
    const p = Promise.resolve({ stdout: "", exitCode: 0 }) as any
    p.quiet = vi.fn().mockReturnValue(p)
    return p
  }

  beforeEach(() => {
    mock$.mockImplementation(() => mkPromise())
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  function callArgs(i: number) {
    return mock$.mock.calls[i] as [TemplateStringsArray, ...any[]]
  }

  function reconstructCmd(i: number): string {
    const [template, ...values] = callArgs(i)
    let cmd = ""
    for (let i = 0; i < template.length; i++) {
      cmd += template[i]
      if (i < values.length) cmd += values[i]
    }
    return cmd
  }

  function expectQuietCalled(i: number) {
    const promise = mock$.mock.results[i].value
    expect(promise.quiet).toHaveBeenCalledOnce()
  }

  describe("plugin setup", () => {
    it("returns hooks object with expected keys", async () => {
      const hooks = await ConfabSync({ $: mock$, serverUrl })
      expect(hooks).toHaveProperty("event")
      expect(hooks).toHaveProperty("dispose")
    })
  })

  describe("session.created event", () => {
    it("spawns daemon for a new session", async () => {
      const hooks = await ConfabSync({ $: mock$, serverUrl })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "test-session-1", directory: "/home/user/project" },
          },
        },
      })

      expect(mock$).toHaveBeenCalledTimes(1)
      const cmd = reconstructCmd(0)
      expect(cmd).toContain("confab hook session-start --provider opencode")
      expect(cmd).toContain('"session_id":"test-session-1"')
      expect(cmd).toContain('"server_url":"http://localhost:4096/')
      expect(cmd).toContain('"cwd":"/home/user/project"')
      expectQuietCalled(0)
    })

    it("does not spawn duplicate daemon for same session", async () => {
      const hooks = await ConfabSync({ $: mock$, serverUrl })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "dup-session", directory: "/tmp" },
          },
        },
      })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "dup-session", directory: "/tmp" },
          },
        },
      })

      expect(mock$).toHaveBeenCalledTimes(1)
    })

    it("spawns separate daemons for different sessions", async () => {
      const hooks = await ConfabSync({ $: mock$, serverUrl })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "session-a", directory: "/tmp" },
          },
        },
      })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "session-b", directory: "/tmp" },
          },
        },
      })

      expect(mock$).toHaveBeenCalledTimes(2)
      expectQuietCalled(0)
      expectQuietCalled(1)
    })

    it("forwards parent_id for subagent (non-root) sessions", async () => {
      const hooks = await ConfabSync({ $: mock$, serverUrl })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "child-session", directory: "/tmp", parentID: "root-session" },
          },
        },
      })
      const cmd = reconstructCmd(0)
      expect(cmd).toContain('"parent_id":"root-session"')
    })

    it("omits parent_id for root sessions", async () => {
      const hooks = await ConfabSync({ $: mock$, serverUrl })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "root-session", directory: "/tmp" },
          },
        },
      })
      const cmd = reconstructCmd(0)
      expect(cmd).not.toContain("parent_id")
    })
  })

  describe("session.idle event (regression)", () => {
    it("does NOT stop daemon — idle fires after every AI response, not session end", async () => {
      const hooks = await ConfabSync({ $: mock$, serverUrl })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "idle-test", directory: "/tmp" },
          },
        },
      })
      mock$.mockClear()

      await hooks.event!({
        event: {
          type: "session.idle",
          properties: { sessionID: "idle-test" },
        },
      })

      expect(mock$).not.toHaveBeenCalled()
    })
  })

  describe("dispose", () => {
    it("stops all active sessions", async () => {
      const hooks = await ConfabSync({ $: mock$, serverUrl })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "session-1", directory: "/tmp" },
          },
        },
      })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "session-2", directory: "/tmp" },
          },
        },
      })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: {
            info: { id: "session-3", directory: "/tmp" },
          },
        },
      })
      mock$.mockClear()

      await hooks.dispose!()

      expect(mock$).toHaveBeenCalledTimes(3)
      const cmds = [0, 1, 2].map((i) => reconstructCmd(i))
      for (const cmd of cmds) {
        expect(cmd).toContain("confab hook session-end")
      }
      expectQuietCalled(0)
      expectQuietCalled(1)
      expectQuietCalled(2)
    })

    it("does nothing when no sessions are active", async () => {
      const hooks = await ConfabSync({ $: mock$, serverUrl })
      await hooks.dispose!()

      expect(mock$).not.toHaveBeenCalled()
    })
  })
})
