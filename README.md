# whatsapp-server

Build side of the home-cluster WhatsApp MCP server. The Kubernetes manifests, the ingress route and the operating notes (pairing, troubleshooting, ideas) are in the `smart-home-config` repository, under `workloads/whatsapp/`. This repository holds only what is built into container images.

| Path | What |
|---|---|
| `.github/workflows/whatsapp-mcp-image.yml` | Manual workflow. Builds [Sealjay/mcp-whatsapp](https://github.com/Sealjay/mcp-whatsapp) at a pinned upstream tag (input `ref`, default `v0.6.0`) for `linux/amd64` and pushes `wilkobets/whatsapp-mcp:<ref>`. The log shows the image's user and home, which the Deployment's `runAsUser`, `fsGroup` and mount path must match |
| `.github/workflows/whatsapp-sidecar-image.yml` | Manual workflow. Builds `sidecar/` for `linux/amd64` and pushes `wilkobets/whatsapp-mqtt-sidecar:<tag>` (input `tag`, default `v0.1.0`) |
| `sidecar/` | A small Go program (`main.go`, commented for someone new to Go) that watches the server's `messages.db` and publishes each new incoming message to MQTT at `whatsapp/incoming/<chat jid>`. Runs as the `mqtt-sidecar` container in the same pod as the server |

Both workflows need the repository secrets `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`.

## After a build

Update the image tag in `smart-home-config/workloads/whatsapp/deployment.yaml` and apply it there. Build the sidecar image before applying a Deployment that references it, or the pod never becomes ready and the MCP endpoint goes down with it.

## Status

The sidecar has not been compiled or run yet; the first workflow run is its first build and may need fixes. The Dockerfile fetches the latest dependency versions at build time (see the comment in `sidecar/Dockerfile` for how to pin them).
