import { describe, it, expect, vi, beforeEach, afterEach } from "vitest"
import { ConfabSync } from "./confab-sync"

describe("ConfabSync", () => {
  const mock$ = vi.fn() as any

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
      const hooks = await ConfabSync({ $: mock$ })
      expect(hooks).toHaveProperty("event")
      expect(hooks).toHaveProperty("dispose")
    })
  })

  describe("session.created event", () => {
    it("spawns daemon for a new session", async () => {
      const hooks = await ConfabSync({ $: mock$ })
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
      expect(cmd).not.toContain("server_url")
      expect(cmd).toContain('"cwd":"/home/user/project"')
      expectQuietCalled(0)
    })

    it("does not spawn duplicate daemon for same session", async () => {
      const hooks = await ConfabSync({ $: mock$ })
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
      const hooks = await ConfabSync({ $: mock$ })
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
      const hooks = await ConfabSync({ $: mock$ })
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
      const hooks = await ConfabSync({ $: mock$ })
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
      const hooks = await ConfabSync({ $: mock$ })
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
      const hooks = await ConfabSync({ $: mock$ })
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
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.dispose!()

      expect(mock$).not.toHaveBeenCalled()
    })
  })

  // CF-549 resume signal: any session.* event on the allowlist triggers a
  // spawn for an active session. The allowlist is hardcoded — new event
  // types upstream must be reviewed and explicitly added.
  describe("resume signal", () => {
    it("session.status spawns daemon with empty cwd and authoritative parent_pid", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: {
          type: "session.status",
          properties: { sessionID: "ses_resume_status", status: "busy" },
        } as any,
      })

      expect(mock$).toHaveBeenCalledTimes(1)
      const cmd = reconstructCmd(0)
      expect(cmd).toContain("confab hook session-start --provider opencode")
      expect(cmd).toContain('"session_id":"ses_resume_status"')
      expect(cmd).toContain('"cwd":""')
      expect(cmd).toContain(`"parent_pid":${process.pid}`)
      expect(cmd).not.toContain("parent_id")
    })

    it("session.updated spawns daemon", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: { type: "session.updated", properties: { sessionID: "ses_updated" } } as any,
      })
      expect(mock$).toHaveBeenCalledTimes(1)
    })

    it("session.compacted spawns daemon (covers /compact-on-resume)", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: { type: "session.compacted", properties: { sessionID: "ses_compact" } } as any,
      })
      expect(mock$).toHaveBeenCalledTimes(1)
    })

    it("session.error spawns daemon (covers error-on-resume)", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: {
          type: "session.error",
          properties: { sessionID: "ses_err", error: "boom" },
        } as any,
      })
      expect(mock$).toHaveBeenCalledTimes(1)
    })

    it("session.deleted does NOT spawn (would shell out for a dead row)", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: { type: "session.deleted", properties: { sessionID: "ses_del" } } as any,
      })
      expect(mock$).not.toHaveBeenCalled()
    })

    it("session.diff does NOT spawn (off the allowlist; unclear semantics)", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: { type: "session.diff", properties: { sessionID: "ses_diff" } } as any,
      })
      expect(mock$).not.toHaveBeenCalled()
    })

    it("session.idle does NOT spawn (off the allowlist; deprecated + redundant with session.status)", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: { type: "session.idle", properties: { sessionID: "ses_idle_off_list" } } as any,
      })
      expect(mock$).not.toHaveBeenCalled()
    })

    it("message.updated does NOT spawn (allowlist enforcement, F3 mitigation)", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: {
          type: "message.updated",
          properties: { sessionID: "ses_msg", messageID: "msg_1" },
        } as any,
      })
      expect(mock$).not.toHaveBeenCalled()
    })

    it("dedups session.status then session.updated for the same session", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: { type: "session.status", properties: { sessionID: "ses_dup", status: "busy" } } as any,
      })
      await hooks.event!({
        event: { type: "session.updated", properties: { sessionID: "ses_dup" } } as any,
      })
      expect(mock$).toHaveBeenCalledTimes(1)
    })

    it("dedups session.created then session.status; preserves inline cwd", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: { info: { id: "ses_combo", directory: "/inline/dir" } },
        } as any,
      })
      await hooks.event!({
        event: { type: "session.status", properties: { sessionID: "ses_combo", status: "busy" } } as any,
      })
      expect(mock$).toHaveBeenCalledTimes(1)
      const cmd = reconstructCmd(0)
      expect(cmd).toContain('"cwd":"/inline/dir"')
    })

    it("dedups concurrent invocations via Promise.all (atomic has/add)", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await Promise.all([
        hooks.event!({
          event: { type: "session.status", properties: { sessionID: "ses_race", status: "busy" } } as any,
        }),
        hooks.event!({
          event: { type: "session.status", properties: { sessionID: "ses_race", status: "idle" } } as any,
        }),
      ])
      expect(mock$).toHaveBeenCalledTimes(1)
    })

    it("includes parent_pid on session.created spawns too (not just reconcile)", async () => {
      const hooks = await ConfabSync({ $: mock$ })
      await hooks.event!({
        event: {
          type: "session.created",
          properties: { info: { id: "ses_created", directory: "/work" } },
        } as any,
      })
      expect(mock$).toHaveBeenCalledTimes(1)
      expect(reconstructCmd(0)).toContain(`"parent_pid":${process.pid}`)
    })

    it("enforces MAX_DAEMONS=32 with a console.error skip on the 33rd distinct session", async () => {
      const errSpy = vi.spyOn(console, "error").mockImplementation(() => {})
      const hooks = await ConfabSync({ $: mock$ })
      for (let i = 0; i < 33; i++) {
        await hooks.event!({
          event: { type: "session.status", properties: { sessionID: `ses_cap_${i}`, status: "busy" } } as any,
        })
      }
      expect(mock$).toHaveBeenCalledTimes(32)
      expect(errSpy).toHaveBeenCalled()
      const calls = errSpy.mock.calls.map((c) => c.join(" "))
      expect(calls.some((c) => c.includes("32") && c.includes("ses_cap_32"))).toBe(true)
      errSpy.mockRestore()
    })
  })
})
