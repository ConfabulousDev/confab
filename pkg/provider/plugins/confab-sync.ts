import type { Plugin } from "@opencode-ai/plugin"

export const ConfabSync: Plugin = async ({ $, serverUrl }) => {
  const running = new Set<string>()

  async function spawn(sessionID: string, cwd: string, parentID?: string) {
    if (running.has(sessionID)) return
    running.add(sessionID)
    const payload: Record<string, unknown> = {
      session_id: sessionID,
      server_url: serverUrl.href,
      cwd,
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
    const input = JSON.stringify({
      session_id: sessionID,
      server_url: serverUrl.href,
    })
    try {
      await $`echo ${input} | confab hook session-end --provider opencode`.quiet()
    } catch (err) {
      // Don't let one failed stop abort shutdown of the remaining sessions.
      console.error(`[confab] failed to stop sync daemon for ${sessionID}:`, err)
    }
  }

  return {
    event: async ({ event }) => {
      if (event.type === "session.created") {
        const session = event.properties.info
        await spawn(session.id, session.directory, session.parentID)
      }
    },
    dispose: async () => {
      for (const sid of [...running]) {
        await stop(sid)
      }
    },
  }
}
