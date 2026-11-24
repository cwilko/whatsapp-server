# whatsapp-server

Builds the images for the home lab's WhatsApp MCP server: the upstream Sealjay/mcp-whatsapp server at a pinned tag, and the `sidecar/` Go program that publishes incoming messages to MQTT. See `README.md`.

## Docs

How the server and sidecar work: `../smart-home-docs/systems/whatsapp.md`. Deploy, pairing and troubleshooting: `../smart-home-docs/runbooks/whatsapp.md`. Manifests: `../smart-home-config/workloads/whatsapp/`. Open issues: `gh issue list --repo cwilko/smart-home-docs --label area:whatsapp`. After a change, update the docs following `../smart-home-docs/CLAUDE.md`, or run `/homelab-wrapup`.

## Rules

- Images are built by the manual GitHub Actions workflows and must target `linux/amd64`.
- After a new build, bump the image tag in `../smart-home-config/workloads/whatsapp/deployment.yaml` and apply it there. Build the sidecar image before applying a Deployment that references it.
- The sidecar opens `whatsapp.db` (the login keys) read-only for one `SELECT`; keep it that way, and never log message text.
