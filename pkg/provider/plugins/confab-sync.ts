import type { Plugin } from "@opencode-ai/plugin"

// Cap protects against pathological event storms (e.g., a scripted bot
// opening hundreds of sessions). Well above any realistic human workflow.
// CF-549 F-up C.
const MAX_DAEMONS = 32

// Allowlist of event types that signal "this session is active and may
// have data to sync". Chosen from the current OpenCode type stub.
// Session-only and tight:
//   - message.* events are redundant (every meaningful message is
//     bracketed by a session.status transition in opencode's flow).
//   - session.idle is upstream-deprecated AND redundant with
//     session.status(idle), which fires alongside it.
//   - session.diff has unclear semantics; conservative skip.
// New upstream event types default-deny — we add them here after reviewing.
// CF-549 F3 mitigation.
const RECONCILE_EVENTS = new Set([
  "session.compacted",
  "session.error",
  "session.status",
  "session.updated",
])

export const ConfabSync: Plugin = async ({ $ }) => {
  const running = new Set<string>()

  async function spawn(sessionID: string, cwd: string, parentID?: string) {
    if (running.has(sessionID)) return
    if (running.size >= MAX_DAEMONS) {
      console.error(`[confab] daemon cap ${MAX_DAEMONS} reached, skipping ${sessionID}`)
      return
    }
    running.add(sessionID)
    const payload: Record<string, unknown> = {
      session_id: sessionID,
      cwd,
      parent_pid: process.pid, // CF-549 M1: opencode PID, authoritative
    }
    // Forward the session's parent id (subagents only) so the CLI can suppress
    // daemons for non-root sessions; omitted for root sessions.
    if (parentID) payload.parent_id = parentID
    const input = JSON.stringify(payload)
    try {
      await $`echo ${input} | confab hook session-start --provider opencode`.quiet()
    } catch (err) {
      // Spawn failed (e.g. confab not on PATH). Drop the session from the
      // running set so dispose doesn't try to stop a daemon that never
      // started, and a later event can retry.
      running.delete(sessionID)
      console.error(`[confab] failed to start sync daemon for ${sessionID}:`, err)
    }
  }

  async function stop(sessionID: string) {
    if (!running.has(sessionID)) return
    running.delete(sessionID)
    const input = JSON.stringify({ session_id: sessionID })
    try {
      await $`echo ${input} | confab hook session-end --provider opencode`.quiet()
    } catch (err) {
      // Don't let one failed stop abort shutdown of the remaining sessions.
      console.error(`[confab] failed to stop sync daemon for ${sessionID}:`, err)
    }
  }

  return {
    event: async ({ event }) => {
      // Fast path: session.created carries inline directory + parentID,
      // no SQLite lookup needed. Stays separate from the allowlist to
      // preserve the cost-zero brand-new-session path.
      if (event.type === "session.created") {
        const session = event.properties.info
        await spawn(session.id, session.directory, session.parentID)
        return
      }
      // Allowlisted reconcile events. Anything not on the list is
      // silently ignored — including session.deleted (where spawning
      // would shell out, read a missing row, then 404 against the
      // backend), session.diff (unclear semantics), and any future
      // event we haven't reviewed.
      if (!RECONCILE_EVENTS.has(event.type)) return
      const props = event.properties as Record<string, unknown>
      if (typeof props.sessionID === "string") {
        await spawn(props.sessionID, "", undefined)
      }
    },
    dispose: async () => {
      for (const sid of [...running]) {
        await stop(sid)
      }
    },
  }
}
