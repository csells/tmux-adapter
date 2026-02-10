# Gastown Tmux Integration Spec

Reference document for how gastown uses tmux as its execution environment and communication channel. All claims trace directly to source code in the gastown repository.

**Source files:**

| File | Lines | Purpose |
|------|-------|---------|
| `internal/tmux/tmux.go` | 1886 | Core tmux wrapper — all operations |
| `internal/tmux/theme.go` | 90 | Color themes for sessions |
| `internal/tmux/process_group_unix.go` | 57 | Process tree cleanup (Unix) |
| `internal/session/names.go` | 61 | Session naming conventions |
| `internal/config/agents.go` | 523 | Agent registry, process detection, runtime config |
| `internal/config/types.go` | ~640 | RuntimeConfig, TmuxConfig types |
| `internal/constants/constants.go` | 263 | Timing constants, role names, shell list |

---

## 1. Overview

Gastown is a multi-agent orchestration system for AI coding agents. It uses tmux as both the execution environment (each agent runs in its own tmux session) and the communication channel (text is sent via `send-keys`, output is read via `capture-pane`).

**Agent hierarchy:**
- **Mayor** — town-level coordinator
- **Deacon** — town-level health monitor
- **Overseer** — human operator session
- **Witness** — per-rig observer
- **Refinery** — per-rig merge processor
- **Polecats** — per-rig transient workers
- **Crew** — per-rig established workers
- **Dogs** — auxiliary workers

The `Tmux` struct (`tmux.go:37`) is a stateless wrapper — every method shells out to the `tmux` binary. There is no persistent connection, no control mode, and no pipe-pane usage. All tmux commands are prepended with `-u` for UTF-8 support (`tmux.go:48-49`).

---

## 2. Session Naming Conventions

Source: `internal/session/names.go`

Two prefixes partition the namespace:

| Prefix | Scope | Constant |
|--------|-------|----------|
| `hq-` | Town-level agents (one per machine) | `HQPrefix = "hq-"` |
| `gt-` | Rig-level agents (per-rig) | `Prefix = "gt-"` |

**Session name functions:**

| Function | Pattern | Example |
|----------|---------|---------|
| `MayorSessionName()` | `hq-mayor` | `hq-mayor` |
| `DeaconSessionName()` | `hq-deacon` | `hq-deacon` |
| `OverseerSessionName()` | `hq-overseer` | `hq-overseer` |
| `WitnessSessionName(rig)` | `gt-{rig}-witness` | `gt-gastown-witness` |
| `RefinerySessionName(rig)` | `gt-{rig}-refinery` | `gt-gastown-refinery` |
| `CrewSessionName(rig, name)` | `gt-{rig}-crew-{name}` | `gt-gastown-crew-max` |
| `PolecatSessionName(rig, name)` | `gt-{rig}-{name}` | `gt-gastown-Toast` |
| `BootSessionName()` | `gt-boot` | `gt-boot` |

**Special case — `gt-boot`:** The Boot watchdog uses `gt-boot` instead of `hq-deacon-boot` to avoid tmux prefix-matching collisions. Tmux matches session names by prefix, so `hq-deacon-boot` would cause `HasSession("hq-deacon")` to return true when only Boot is running (`names.go:53-58`).

**Session name validation:** All session names must match `^[a-zA-Z0-9_-]+$` to prevent shell injection (`tmux.go:27`).

---

## 3. Agent-to-Session Mapping

Each agent gets its own isolated tmux session — there is no session sharing between agents. The mapping is 1:1: one agent = one tmux session.

**Session environment variables** are set via `SetEnvironment` (`tmux.go:1154`) and read via `GetEnvironment` (`tmux.go:1160`):

| Variable | Purpose | Example |
|----------|---------|---------|
| `GT_AGENT` | Agent preset name | `claude`, `gemini`, `codex` |
| `GT_ROLE` | Agent role | `mayor`, `witness`, `crew`, `polecat` |
| `GT_RIG` | Rig name (rig-level agents only) | `gastown` |

These are read by `IsAgentAlive` (`tmux.go:1321-1325`) to determine which process names to check for zombie detection.

---

## 4. Agent Runtime Registry

Source: `internal/config/agents.go:107-206`

Seven supported runtimes with their configurations:

| Preset | Command | Args | Process Names | Session ID Env | Resume Style |
|--------|---------|------|---------------|----------------|--------------|
| `claude` | `claude` | `--dangerously-skip-permissions` | `node`, `claude` | `CLAUDE_SESSION_ID` | flag: `--resume` |
| `gemini` | `gemini` | `--approval-mode yolo` | `gemini` | `GEMINI_SESSION_ID` | flag: `--resume` |
| `codex` | `codex` | `--yolo` | `codex` | _(from JSONL)_ | subcommand: `resume` |
| `cursor` | `cursor-agent` | `-f` | `cursor-agent` | _(--resume chatId)_ | flag: `--resume` |
| `auggie` | `auggie` | `--allow-indexing` | `auggie` | — | flag: `--resume` |
| `amp` | `amp` | `--dangerously-allow-all --no-ide` | `amp` | — | subcommand: `threads continue` |
| `opencode` | `opencode` | _(none, uses env)_ | `opencode`, `node`, `bun` | — | — |

**Resume command building** (`agents.go:384-409`):
- **flag style:** `claude --dangerously-skip-permissions --resume <session_id>`
- **subcommand style:** `codex resume <session_id> --yolo`

**Process name resolution** (`agents.go:430-437`): `GetProcessNames(agentName)` returns the process names for an agent. Falls back to `["node", "claude"]` if the agent is unknown or has no ProcessNames configured.

The registry supports user-defined agents via JSON files at `<town>/settings/agents.json` or `<rig>/settings/agents.json`. User definitions override built-ins with the same name (`agents.go:267-269`).

---

## 5. Session Lifecycle

### Creation

**`NewSession(name, workDir)`** (`tmux.go:88-95`):
```go
func (t *Tmux) NewSession(name, workDir string) error {
    args := []string{"new-session", "-d", "-s", name}
    if workDir != "" {
        args = append(args, "-c", workDir)
    }
    _, err := t.run(args...)
    return err
}
```
Creates a detached session (`-d`) with an optional working directory (`-c`).

**`NewSessionWithCommand(name, workDir, command)`** (`tmux.go:102-111`):
Same as `NewSession` but appends the command as the last argument to `new-session`, making it the pane's initial process. This avoids race conditions where `SendKeys` arrives before the shell prompt is ready.

### Health Check

**`EnsureSessionFresh(name, workDir)`** (`tmux.go:122-146`):
1. Check if session exists via `HasSession`
2. If exists and agent is NOT running → zombie; kill with `KillSessionWithProcesses`, then recreate
3. If exists and agent IS running → healthy, do nothing
4. If doesn't exist → create fresh session

### Session Existence

**`HasSession(name)`** (`tmux.go:532-541`):
```go
func (t *Tmux) HasSession(name string) (bool, error) {
    _, err := t.run("has-session", "-t", "="+name)
    // ...
}
```
Uses `=` prefix for exact matching — prevents `gt-deacon-boot` from matching when checking for `gt-deacon`.

**`GetSessionSet()`** (`tmux.go:571-602`): For batch checking, lists all sessions once and returns an O(1) lookup set. Avoids N+1 subprocess calls when checking many sessions.

### Destruction

**`KillSession(name)`** (`tmux.go:149-152`): Simple `kill-session -t name`.

**`KillSessionWithProcesses(name)`** (`tmux.go:176-234`) — the robust version:

```go
func (t *Tmux) KillSessionWithProcesses(name string) error {
    pid, err := t.GetPanePID(name)
    if err != nil {
        return t.KillSession(name)
    }

    if pid != "" {
        // Walk the process tree for all descendants
        descendants := getAllDescendants(pid)

        // Build known PID set for group membership verification
        knownPIDs := make(map[string]bool, len(descendants)+1)
        knownPIDs[pid] = true
        for _, d := range descendants {
            knownPIDs[d] = true
        }

        // Find reparented processes from our process group
        pgid := getProcessGroupID(pid)
        if pgid != "" && pgid != "0" && pgid != "1" {
            reparented := collectReparentedGroupMembers(pgid, knownPIDs)
            descendants = append(descendants, reparented...)
        }

        // SIGTERM to all descendants (deepest first)
        for _, dpid := range descendants {
            _ = exec.Command("kill", "-TERM", dpid).Run()
        }

        // Wait for graceful shutdown
        time.Sleep(processKillGracePeriod) // 2 seconds

        // SIGKILL to remaining descendants
        for _, dpid := range descendants {
            _ = exec.Command("kill", "-KILL", dpid).Run()
        }

        // Kill the pane process itself
        _ = exec.Command("kill", "-TERM", pid).Run()
        time.Sleep(processKillGracePeriod)
        _ = exec.Command("kill", "-KILL", pid).Run()
    }

    err = t.KillSession(name)
    if err == ErrSessionNotFound {
        return nil
    }
    return err
}
```

Multi-phase destruction:
1. Get pane PID
2. Walk descendant tree recursively (deepest-first via `getAllDescendants`)
3. Find reparented group members (processes with PPID=1 sharing our PGID)
4. SIGTERM all descendants
5. Wait 2s (`processKillGracePeriod`)
6. SIGKILL remaining descendants
7. SIGTERM → 2s → SIGKILL the pane process itself
8. `kill-session` (ignore "not found" — killing processes may auto-destroy session)

**Self-exclusion variant:** `KillSessionWithProcessesExcluding(name, excludePIDs)` (`tmux.go:240-322`) — same logic but skips specified PIDs. Essential for `gt done` scenarios where the calling process runs inside the session being terminated.

---

## 6. Sending Text

### NudgeSession — The Canonical Method

Source: `tmux.go:780-821`

This is the primary way gastown sends messages to agent sessions. All other send methods are lower-level building blocks.

```go
func (t *Tmux) NudgeSession(session, message string) error {
    // Serialize nudges to this session to prevent interleaving
    lock := getSessionNudgeLock(session)
    lock.Lock()
    defer lock.Unlock()

    // Resolve correct pane in multi-pane sessions
    target := session
    if agentPane, err := t.FindAgentPane(session); err == nil && agentPane != "" {
        target = agentPane
    }

    // 1. Send text in literal mode
    if _, err := t.run("send-keys", "-t", target, "-l", message); err != nil {
        return err
    }

    // 2. Wait 500ms for paste to complete
    time.Sleep(500 * time.Millisecond)

    // 3. Send Escape (for vim mode)
    _, _ = t.run("send-keys", "-t", target, "Escape")
    time.Sleep(100 * time.Millisecond)

    // 4. Send Enter with 3x retry
    var lastErr error
    for attempt := 0; attempt < 3; attempt++ {
        if attempt > 0 {
            time.Sleep(200 * time.Millisecond)
        }
        if _, err := t.run("send-keys", "-t", target, "Enter"); err != nil {
            lastErr = err
            continue
        }
        // 5. Wake pane via SIGWINCH
        t.WakePaneIfDetached(session)
        return nil
    }
    return fmt.Errorf("failed to send Enter after 3 attempts: %w", lastErr)
}
```

**Key properties of NudgeSession:**

1. **Per-session mutex** — serialized via `sync.Map` of mutexes (`tmux.go:24`). Prevents interleaving when hooks and nudges arrive concurrently.
2. **Multi-pane targeting** — calls `FindAgentPane` to locate the agent pane, avoiding sending to the wrong (focused) pane.
3. **Literal mode** — `send-keys -l` treats text literally (no key name interpretation).
4. **500ms debounce** — required for Claude Code to process the pasted text before Enter.
5. **Escape key** — exits vim INSERT mode if Claude has vi-style bindings. Harmless in normal mode.
6. **100ms post-Escape delay** — lets the mode switch take effect.
7. **Enter with 3x retry** — separate `send-keys Enter` (not appended to text). Retries with 200ms backoff.
8. **SIGWINCH wake** — after successful send, wakes detached sessions via resize dance.

**NudgePane** (`tmux.go:827-861`): Same pattern but targets a pane ID (e.g., `%9`) instead of session name.

### SendKeys Variants

| Method | Behavior | Source |
|--------|----------|--------|
| `SendKeys(session, keys)` | Literal send + 100ms debounce + Enter | `tmux.go:665-667` |
| `SendKeysDebounced(session, keys, ms)` | Literal send + custom debounce + Enter | `tmux.go:672-684` |
| `SendKeysRaw(session, keys)` | Raw send, no `-l`, no Enter | `tmux.go:687-690` |
| `SendKeysReplace(session, keys, ms)` | Ctrl-U clear + delay + SendKeys | `tmux.go:696-709` |
| `SendKeysDelayed(session, keys, ms)` | Sleep + SendKeys | `tmux.go:713-716` |
| `SendKeysDelayedDebounced(session, keys, pre, deb)` | Pre-delay + custom debounce send | `tmux.go:723-728` |

Note: `SendKeys` uses `DefaultDebounceMs = 500` (from `constants.go:22`), but `SendKeysDebounced` allows callers to specify their own debounce.

### Timing Constants

| Constant | Value | Source | Purpose |
|----------|-------|--------|---------|
| `DefaultDebounceMs` | 500ms | `constants.go:22` | Paste-to-Enter debounce for SendKeys |
| `NudgeSession` paste wait | 500ms | `tmux.go:799` | Paste-to-Enter debounce (hardcoded) |
| `NudgeSession` Escape-to-Enter | 100ms | `tmux.go:804` | Post-Escape delay |
| `NudgeSession` Enter retry backoff | 200ms | `tmux.go:810` | Between Enter retries |
| `NudgeSession` max Enter retries | 3 | `tmux.go:808` | Enter retry limit |
| `PollInterval` | 100ms | `constants.go:28` | Wait loop polling |
| `processKillGracePeriod` | 2s | `tmux.go:157` | SIGTERM-to-SIGKILL grace |
| `ClaudeStartTimeout` | 60s | `constants.go:14` | Max wait for agent startup |
| `ShellReadyTimeout` | 5s | `constants.go:17` | Max wait for shell prompt |
| `GracefulShutdownTimeout` | 3s | `constants.go:34` | Post-Ctrl-C grace period |
| `DefaultDisplayMs` | 5000ms | `constants.go:25` | Status bar message duration |

---

## 7. Capturing Output

Gastown uses a polling-based approach — it calls `capture-pane` on demand. There is no `pipe-pane`, no control mode, and no event-driven output capture.

### Capture Methods

**`CapturePane(session, lines)`** (`tmux.go:1119-1121`):
```go
func (t *Tmux) CapturePane(session string, lines int) (string, error) {
    return t.run("capture-pane", "-p", "-t", session, "-S", fmt.Sprintf("-%d", lines))
}
```
Captures the last N lines. The `-p` flag prints to stdout (instead of to a buffer). `-S -N` means start N lines before the current position.

**`CapturePaneAll(session)`** (`tmux.go:1124-1126`):
```go
func (t *Tmux) CapturePaneAll(session string) (string, error) {
    return t.run("capture-pane", "-p", "-t", session, "-S", "-")
}
```
Captures the entire scrollback history. `-S -` means from the very beginning.

**`CapturePaneLines(session, lines)`** (`tmux.go:1129-1138`):
Returns `CapturePane` output split into a `[]string` slice.

### ZFC Principle

Gastown follows the "Zero False Commands" (ZFC) principle: regex-based output detection is only acceptable during bootstrap (cold startup). Once any AI agent is running, observation should be AI-to-AI via commands like `gt peek`.

`WaitForRuntimeReady` (`tmux.go:1397-1434`) is the one ZFC exception — it polls `CapturePaneLines` looking for a prompt prefix. This is only used during initial agent startup, not during steady-state operation.

---

## 8. Agent Detection

### IsAgentRunning

`tmux.go:1248-1274` — Checks `pane_current_command` via `GetPaneCommand`. If `expectedPaneCommands` is provided, checks for exact match. Otherwise, any non-shell command counts as "running".

### IsRuntimeRunning

`tmux.go:1279-1315` — The unified detection method for all agent types:
1. Get `pane_current_command`
2. Check direct match against `processNames`
3. If pane command is a shell → walk descendants via `hasDescendantWithNames`
4. If pane command is unrecognized → check if the process binary itself matches (handles version-as-argv[0] like Claude showing "2.1.30")
5. Final fallback: check descendants

### IsAgentAlive

`tmux.go:1321-1325` — The preferred high-level method:
```go
func (t *Tmux) IsAgentAlive(session string) bool {
    agentName, _ := t.GetEnvironment(session, "GT_AGENT")
    processNames := config.GetProcessNames(agentName)
    return t.IsRuntimeRunning(session, processNames)
}
```
Reads `GT_AGENT` from session environment to determine which process names to check. Falls back to Claude defaults (`["node", "claude"]`) if `GT_AGENT` is not set.

### FindAgentPane

`tmux.go:923-972` — For multi-pane sessions, enumerates all panes and returns the pane ID running the agent:

```go
func (t *Tmux) FindAgentPane(session string) (string, error) {
    out, err := t.run("list-panes", "-t", session, "-F",
        "#{pane_id}\t#{pane_current_command}\t#{pane_pid}")
    // ...
    lines := strings.Split(strings.TrimSpace(out), "\n")
    if len(lines) <= 1 {
        return "", nil  // Single pane — no disambiguation needed
    }

    agentName, _ := t.GetEnvironment(session, "GT_AGENT")
    processNames := config.GetProcessNames(agentName)

    for _, line := range lines {
        // ... parse paneID, paneCmd, panePID
        // Direct command match
        // Shell with agent descendant
        // Version-as-argv[0] check
    }
    return "", nil  // No agent pane found
}
```

Detection priority:
1. Direct `pane_current_command` match against process names
2. Shell pane with agent descendant (via `hasDescendantWithNames`, max depth 10)
3. Process binary name match (via `processMatchesNames` using `ps -p PID -o comm=`)

### Descendant Walking

`hasDescendantWithNames` (`tmux.go:1035-1074`): Recursively checks process tree via `pgrep -P <pid> -l`. Max depth = 10. Returns true if any descendant matches the target process names.

`getAllDescendants` (`tmux.go:352-370`): Returns all descendant PIDs in deepest-first order (for kill ordering). Uses `pgrep -P <pid>`.

---

## 9. Session Configuration

All session configuration is done programmatically — there is no `.tmux.conf` dependency.

### ConfigureGasTownSession

`tmux.go:1556-1582` — Applies the complete gastown look:
1. `ApplyTheme` — status bar colors
2. `SetStatusFormat` — left status (icon + identity)
3. `SetDynamicStatus` — right status (shell command refreshed every 5s)
4. `SetMailClickBinding` — click status-right for mail
5. `SetFeedBinding` — `C-b a` for activity feed
6. `SetAgentsBinding` — `C-b g` for agent switcher
7. `SetCycleBindings` — `C-b n/p` for session cycling
8. `EnableMouseMode` — mouse + clipboard

### Session Options Applied

| Option | Value | Purpose | Source |
|--------|-------|---------|--------|
| `status-style` | `bg=COLOR,fg=COLOR` | Theme colors | `tmux.go:1482` |
| `status-left` | icon + identity | Agent identification | `tmux.go:1527` |
| `status-left-length` | `25` | Prevent truncation | `tmux.go:1524` |
| `status-right` | `#(gt status-line ...)` | Dynamic status | `tmux.go:1541` |
| `status-right-length` | `80` | Wide status area | `tmux.go:1543` |
| `status-interval` | `5` | 5-second refresh | `tmux.go:1547` |
| `mouse` | `on` | Mouse support | `tmux.go:1589` |
| `set-clipboard` | `on` | OSC 52 clipboard | `tmux.go:1594` |
| `remain-on-exit` | `on` (for auto-respawn) | Keep pane after exit | `tmux.go:1647-1654` |
| `exit-empty` | `on`/`off` (configurable) | Server exit behavior | `tmux.go:1511-1521` |

### Keybindings

All bindings are conditional — they only activate for gastown sessions (names matching `^(gt|hq)-`). Non-gastown sessions get default tmux behavior.

| Binding | Action (GT sessions) | Fallback | Source |
|---------|---------------------|----------|--------|
| `C-b n` | `gt cycle next --session #{session_name}` | `next-window` | `tmux.go:1698-1701` |
| `C-b p` | `gt cycle prev --session #{session_name}` | `previous-window` | `tmux.go:1705-1708` |
| `C-b a` | `gt feed --window` | display help message | `tmux.go:1723-1726` |
| `C-b g` | `gt agents` | display help message | `tmux.go:1737-1740` |
| MouseDown1StatusRight | `display-popup -E -w 60 -h 15 "gt mail peek"` | — | `tmux.go:1609-1611` |

The conditional check uses tmux `if-shell` with: `echo '#{session_name}' | grep -Eq '^(gt|hq)-'`

---

## 10. Process Cleanup

### KillSessionWithProcesses — Full Strategy

Source: `tmux.go:176-234`, `process_group_unix.go`

**Why multi-phase?** `kill-session` sends SIGHUP to processes, but many agents ignore SIGHUP. Direct process killing is required.

**Phase 1 — Build kill list:**
- `getAllDescendants(pid)` — recursive `pgrep -P` walk, returns deepest-first
- `getProcessGroupID(pid)` — `ps -o pgid= -p PID`
- `collectReparentedGroupMembers(pgid, knownPIDs)` — finds processes that:
  - Share our PGID
  - Are NOT already in the descendant tree
  - Have PPID == 1 (reparented to init, likely our former children)

**Phase 2 — Kill:**
- SIGTERM all descendants (deepest-first to avoid orphaning)
- Wait `processKillGracePeriod` (2 seconds)
- SIGKILL remaining descendants
- SIGTERM → 2s → SIGKILL the pane process itself
- `kill-session` (ignore "not found")

**Why not kill the entire process group?** `collectReparentedGroupMembers` (`tmux.go:332-348`) is deliberately conservative — it only kills group members reparented to init (PPID=1). Blindly killing `-pgid` could hit unrelated processes sharing the same PGID.

### Grace Period

```go
const processKillGracePeriod = 2 * time.Second  // tmux.go:157
```
The previous value (100ms) was too short — Claude processes became orphans when they couldn't shut down in time.

---

## 11. Wake Mechanism

Source: `tmux.go:752-768`

**Problem:** When Claude runs in a detached tmux session, its TUI library (ink/react) may not process stdin until a terminal event occurs. Attaching triggers SIGWINCH which wakes the event loop.

**Solution — resize dance:**
```go
func (t *Tmux) WakePane(target string) {
    _, _ = t.run("resize-pane", "-t", target, "-y", "-1")
    time.Sleep(50 * time.Millisecond)
    _, _ = t.run("resize-pane", "-t", target, "-y", "+1")
}
```
Shrink by 1 row, wait 50ms, restore. This triggers SIGWINCH without changing the final pane size.

**`WakePaneIfDetached(target)`** (`tmux.go:763-768`): Only wakes if `IsSessionAttached` returns false. Avoids unnecessary latency when a human is watching.

**`IsSessionAttached(target)`** (`tmux.go:738-741`): Checks `#{session_attached}` via `display-message`.

---

## 12. Hooks & Auto-Recovery

### Crash Logging Hook

**`SetPaneDiedHook(session, agentID)`** (`tmux.go:1812-1826`):
Sets a per-session `pane-died` hook that logs crashes:
```
run-shell "gt log crash --agent '<agentID>' --session '<session>' --exit-code #{pane_dead_status}"
```
`#{pane_dead_status}` is the exit code of the process that died.

### Auto-Respawn Hook

**`SetAutoRespawnHook(session)`** (`tmux.go:1838-1860`):
For persistent agents (like Deacon) that should never exit:
1. Enables `remain-on-exit` on (pane stays after process exit)
2. Sets `pane-died` hook:
```
run-shell "sleep 3 && tmux respawn-pane -k -t '<session>' && tmux set-option -t '<session>' remain-on-exit on"
```

Key details:
- **3-second sleep** — debounces rapid crash loops
- **`respawn-pane -k`** — kills remaining processes and restarts with original command
- **Re-enable `remain-on-exit`** — `respawn-pane` resets it to off, so it must be re-enabled after each respawn

### Global vs Per-Session Hooks

Global `pane-died` hooks don't fire reliably in tmux 3.2a (`tmux.go:1864-1885`). Gastown uses per-session hooks exclusively (`set-hook -t <session>` not `set-hook -g`).

---

## 13. Tmux Commands Reference

Every tmux subcommand used by gastown, organized by category. All commands are invoked as `tmux -u <subcommand> [args...]`.

### Session Management

| Command | Usage | Source |
|---------|-------|--------|
| `new-session -d -s NAME [-c DIR] [CMD]` | Create detached session | `tmux.go:89,103` |
| `kill-session -t NAME` | Destroy session | `tmux.go:150` |
| `has-session -t =NAME` | Exact-match existence check | `tmux.go:533` |
| `list-sessions -F FORMAT [-f FILTER]` | List sessions with format | `tmux.go:545,629,1439` |
| `attach-session -t NAME` | Attach to session | `tmux.go:1143` |
| `switch-client -t NAME` | Switch client to session | `tmux.go:1659` |
| `rename-session -t OLD NEW` | Rename session | `tmux.go:1197` |
| `kill-server` | Kill entire tmux server | `tmux.go:499` |

### Pane Operations

| Command | Usage | Source |
|---------|-------|--------|
| `list-panes -t SESSION -F FORMAT` | List panes with metadata | `tmux.go:906,925,977,989,999` |
| `capture-pane -p -t SESSION -S OFFSET` | Capture pane content to stdout | `tmux.go:1120,1125` |
| `respawn-pane -k -t PANE [CMD]` | Kill + restart pane process | `tmux.go:1618,1626` |
| `resize-pane -t TARGET -y DELTA` | Resize pane (wake mechanism) | `tmux.go:755-757` |
| `clear-history -t PANE` | Clear scrollback buffer | `tmux.go:1639` |

### Key Sending

| Command | Usage | Source |
|---------|-------|--------|
| `send-keys -t TARGET -l TEXT` | Send literal text | `tmux.go:674,794` |
| `send-keys -t TARGET Enter` | Send Enter key | `tmux.go:682,812` |
| `send-keys -t TARGET Escape` | Send Escape key | `tmux.go:803` |
| `send-keys -t TARGET C-u` | Send Ctrl-U (clear line) | `tmux.go:698` |
| `send-keys -t TARGET Down` | Send Down arrow | `tmux.go:888` |

### Configuration

| Command | Usage | Source |
|---------|-------|--------|
| `set-option -t SESSION KEY VALUE` | Set session option | `tmux.go:1482,1524,etc.` |
| `set-option -g KEY VALUE` | Set global option | `tmux.go:516` |
| `set-environment -t SESSION KEY VALUE` | Set session env var | `tmux.go:1155` |
| `show-environment -t SESSION [KEY]` | Read session env var(s) | `tmux.go:1161,1175` |
| `bind-key -T TABLE KEY CMD` | Create key binding | `tmux.go:1609,1698,etc.` |
| `set-hook -t SESSION EVENT CMD` | Set per-session hook | `tmux.go:1824,1854` |
| `set-hook -g EVENT CMD` | Set global hook (unreliable in 3.2a) | `tmux.go:1879` |

### Display

| Command | Usage | Source |
|---------|-------|--------|
| `display-message -t SESSION -p FORMAT` | Query session variables | `tmux.go:739,1217,1747` |
| `display-message -t SESSION -d MS MSG` | Show status bar message | `tmux.go:1217` |
| `display-popup -E -w W -h H CMD` | Show popup window | `tmux.go:1610` |

### Format Variables Used

| Variable | Purpose | Source |
|----------|---------|--------|
| `#{session_name}` | Session name | `tmux.go:545,1699` |
| `#{session_id}` | Session ID (`$N`) | `tmux.go:629` |
| `#{session_attached}` | Whether client is attached | `tmux.go:739` |
| `#{session_created}` | Creation timestamp | `tmux.go:1747` |
| `#{session_windows}` | Window count | `tmux.go:1438` |
| `#{session_activity}` | Last activity time | `tmux.go:1438` |
| `#{session_last_attached}` | Last attach time | `tmux.go:1438` |
| `#{pane_id}` | Pane ID (e.g., `%0`) | `tmux.go:925,977` |
| `#{pane_current_command}` | Current pane command | `tmux.go:906,925` |
| `#{pane_pid}` | Pane process PID | `tmux.go:925,999` |
| `#{pane_current_path}` | Pane working directory | `tmux.go:990` |
| `#{pane_dead_status}` | Exit code of dead pane | `tmux.go:1820` |

---

## 14. Theme System

Source: `internal/tmux/theme.go`

### Default Palette (10 themes)

| Name | Background | Foreground |
|------|-----------|------------|
| ocean | `#1e3a5f` | `#e0e0e0` |
| forest | `#2d5a3d` | `#e0e0e0` |
| rust | `#8b4513` | `#f5f5dc` |
| plum | `#4a3050` | `#e0e0e0` |
| slate | `#4a5568` | `#e0e0e0` |
| ember | `#b33a00` | `#f5f5dc` |
| midnight | `#1a1a2e` | `#c0c0c0` |
| wine | `#722f37` | `#f5f5dc` |
| teal | `#0d5c63` | `#e0e0e0` |
| copper | `#6d4c41` | `#f5f5dc` |

### Special Role Themes

| Role | Background | Foreground |
|------|-----------|------------|
| Mayor | `#3d3200` (gold/dark) | `#ffd700` (gold) |
| Deacon | `#2d1f3d` (purple) | `#c0b0d0` (silver) |
| Dog | `#3d2f1f` (brown) | `#d0c0a0` (tan) |

### Theme Assignment

Rig-level agents use consistent hashing (`fnv.New32a`) on the rig name to pick from the default palette. The same rig always gets the same color (`theme.go:62-75`).

---

## 15. Adapter Implications

Summary of what the tmux-adapter must support to integrate with gastown:

### Discovery
- List all gastown sessions by prefix (`hq-` for town-level, `gt-` for rig-level)
- Map sessions to agent roles via naming conventions (section 2) and `GT_*` env vars (section 3)
- Use `=` prefix for exact session matching to avoid prefix collisions

### Sending Text
- Replicate the NudgeSession pattern (section 6):
  - `send-keys -l` for literal text
  - 500ms debounce before Enter
  - Escape key before Enter (100ms delay)
  - Enter as separate `send-keys` call with 3x retry (200ms backoff)
  - Per-session serialization to prevent interleaving
  - SIGWINCH wake after send for detached sessions

### Capturing Output
- `capture-pane -p -t SESSION -S -N` for last N lines
- `capture-pane -p -t SESSION -S -` for full scrollback
- Polling-based — no pipe-pane, no control mode

### Agent Health
- Replicate the `IsAgentAlive` pattern (section 8):
  - Read `GT_AGENT` from session environment
  - Look up process names from agent registry
  - Check `pane_current_command` for direct match
  - Walk descendant tree for shell-wrapped agents
  - Handle version-as-argv[0] edge case

### Wake Mechanism
- Implement the resize dance for detached sessions:
  - `resize-pane -y -1` → 50ms sleep → `resize-pane -y +1`
  - Only when `#{session_attached}` == `0`

### Process Lifecycle
- Support the multi-phase kill sequence (section 10) if the adapter manages session lifecycle
- Respect the 2-second grace period between SIGTERM and SIGKILL
- Handle self-exclusion for in-session operations

### Environment Variables
- Read: `show-environment -t SESSION KEY` (parse `KEY=value` format)
- Write: `set-environment -t SESSION KEY VALUE`
- Key variables: `GT_AGENT`, `GT_ROLE`, `GT_RIG`

### UTF-8
- All tmux commands must include `-u` flag (prepended before subcommand)

### Supported Shells
- For agent detection, recognize these as "shell" (not agent): `bash`, `zsh`, `sh`, `fish`, `tcsh`, `ksh`
