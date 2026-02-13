---
name: ngrok-start
description: Expose tmux-adapter over the internet via ngrok. Use when the user asks to expose local services via ngrok, test over the internet, share a local server publicly, or set up ngrok tunnels.
---

# ngrok-start

Expose the adapter (and optionally the sample) over the internet via ngrok.

## Preferred: Single Tunnel with --debug-serve-dir

Use `--debug-serve-dir` so the adapter serves the sample too — one port, one tunnel:

```bash
# 1. Start the adapter with the sample
./tmux-adapter --gt-dir ~/gt --port 8080 --debug-serve-dir ./samples

# 2. Expose via ngrok
ngrok http 8080
```

For a stable URL across restarts, claim a free static domain at https://dashboard.ngrok.com/domains:

```bash
ngrok http --url your-name.ngrok-free.app 8080
```

## Alternative: Two Tunnels (separate servers)

If the adapter and sample must run on separate ports, use the expose script:

```bash
bash .claude/skills/ngrok-start/scripts/expose.sh <service-port> <sample-port>
```

The script configures both tunnels in the ngrok config and starts them with `ngrok start --all` (free tier allows only one agent session). It prints a combined URL with `?adapter=` pointing at the service tunnel.

The service needs CORS for the sample's ngrok origin:
```bash
./tmux-adapter --gt-dir ~/gt --port 8080 --allowed-origins "localhost:*,*.ngrok-free.app"
```

## ngrok interstitial

Free tier shows a "Visit Site" interstitial on first load. Users click through it once.

## Troubleshooting

- **ERR_NGROK_108** (multiple agents): Kill all ngrok processes (`pkill -f ngrok`) and retry.
- **Tunnels not appearing**: Query `curl -s http://localhost:4040/api/tunnels` to check status.
- **WebSocket rejected**: Ensure `--allowed-origins` includes `*.ngrok-free.app`.
