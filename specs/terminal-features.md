# Terminal Features

All of the following terminal-like features for interacting with agents have to
work equally well on desktop and mobile browsers.

## Deal with ANSI Streaming Gunk

### Problem

The adapter streams raw pipe-pane bytes to clients. These contain ANSI escape
sequences (cursor movement, screen clears, alternate screen buffer, colors) that
render as gunk in a `<pre>` tag. History from `capture-pane -p -S -` is clean
because tmux resolves the terminal state, but the live stream is raw.

### Solution: Server-Side VT100 Emulation + Structured Diffs

Run a VT100 terminal emulator **on the server** (using `hinshun/vt10x` in Go).
Feed raw pipe-pane bytes through it. Send the **rendered screen state** to
clients as structured JSON. The client just renders styled HTML — works on every
browser, every device, zero dependencies.

```
pipe-pane → raw bytes → server-side VT100 emulator → screen state
                                                         ↓
                                                    row-level diff
                                                         ↓
                                              structured JSON update
                                                         ↓
                                           WebSocket → any client
                                                         ↓
                                              styled <span> grid
                                        (desktop ✓  mobile ✓  zero deps ✓)
```

This is the same approach Mosh (Mobile Shell) uses: server-side terminal state,
client-side rendering. The client never sees a raw escape sequence.

### Architecture

- **VT Screen per agent**: Each pipeStream gets a `vt.Screen` (80×24 by default)
- **Row-level diffing**: After each Write, compare rendered rows to previous
  state. Only changed rows are sent to clients.
- **ANSI SGR encoding**: Each row is encoded as text with SGR escape sequences
  for colors/bold/italic (e.g., `\x1b[1;31mhello\x1b[0m`). No cursor movement
  codes — just styled text. The client only needs to parse `ESC[...m`.
- **Throttled updates**: Diffs are batched at ~30fps (33ms) to prevent flooding
  during rapid terminal redraws.

### Protocol Changes

**subscribe-output response** (adds `screen` field):
```json
{
  "id": "3",
  "type": "subscribe-output",
  "ok": true,
  "history": "... scrollback plain text ...",
  "screen": {
    "rows": {"0": "$ ls -la", "1": "total 42", "2": "..."},
    "cols": 80,
    "numRows": 24,
    "cursor": [5, 10]
  }
}
```

**screen-update event** (replaces `output` event for live streaming):
```json
{
  "type": "screen-update",
  "name": "hq-mayor",
  "rows": {"3": "\u001b[32m$ \u001b[0mls", "5": "README.md"},
  "cursor": [5, 10]
}
```

### Client Rendering

The dashboard displays:
1. **Scrollback** (above): History from capture-pane, plain text in a scrollable
   div. This is the agent's full history.
2. **Live Screen** (below): A fixed-height grid of rows. Each row is parsed for
   SGR codes and rendered as styled `<span>` elements.

The live screen is just HTML — works perfectly on mobile. No canvas, no
keyboard hijacking, no xterm.js dependency.

### Why This Beats the Alternatives

| Approach | Rendering | Mobile | Dependencies |
|----------|-----------|--------|--------------|
| Raw stream (`<pre>`) | Gunk | Works | Zero |
| ANSI strip | No colors | Works | Zero |
| xterm.js | Perfect | Broken | 300KB |
| **Server-side VT** | **Perfect** | **Works** | **~0 (Go lib)** |

---

## Terminal Command

Need to be able to do `!` and switch to terminal mode to enter a command.

## Terminal Mode (ala faux-term)

Need to be able to do `!` and press Enter and switch to a mode that looks like a
terminal, but isn't, executing each line like a terminal command, showing the
output, letting me do another command, etc. until I type the "exit" command.

## File Attachments

Need to be able to attach a file, e.g. image via copy paste, that goes to the
server-side.

## @file Mentions

Need to be able to resolve @file mentions against the actual file system in the
cwd that the agent is running in.

## /command Executions

Need to be able to get a set of commands from each agent and provide for normal
/command-style syntax completion.
