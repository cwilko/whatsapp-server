# whatsapp-server

Build side of the home-cluster WhatsApp MCP server. The Kubernetes manifests and the ingress route are in the `smart-home-config` repository, under `workloads/whatsapp/`. How it works, and the deploy, pairing and troubleshooting steps, are in `smart-home-docs` (`systems/whatsapp.md` and `runbooks/whatsapp.md`). This repository holds only what is built into container images.

| Path | What |
|---|---|
| `.github/workflows/whatsapp-mcp-image.yml` | Manual workflow. Builds [Sealjay/mcp-whatsapp](https://github.com/Sealjay/mcp-whatsapp) at a pinned upstream tag (input `ref`, default `v0.6.0`) for `linux/amd64` and pushes `wilkobets/whatsapp-mcp:<ref>`. The log shows the image's user and home, which the Deployment's `runAsUser`, `fsGroup` and mount path must match |
| `.github/workflows/whatsapp-sidecar-image.yml` | Manual workflow. Builds `sidecar/` for `linux/amd64` and pushes `wilkobets/whatsapp-sidecar:<tag>` (input `tag`, default `v0.1.0`) |
| `sidecar/` | A small Go program (`main.go`, commented for someone new to Go) that watches the server's `messages.db` and publishes each new incoming message, as JSON, to the one MQTT topic given in its `TOPIC` environment variable (no topic logic in the code; the chat is the `chat_jid` field of the payload, with `chat_name` and `sender_name` display names added from the server's contact list). Runs as the `mqtt-sidecar` container in the same pod as the server |

Both workflows need the repository secrets `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`.

## After a build

Update the image tag in `smart-home-config/workloads/whatsapp/deployment.yaml` and apply it there. Build the sidecar image before applying a Deployment that references it, or the pod never becomes ready and the MCP endpoint goes down with it.

## Status

The sidecar is deployed and publishing to MQTT. Its dependency versions are pinned in `sidecar/go.mod` and `sidecar/go.sum`; the comment in `sidecar/Dockerfile` says how to update them.
