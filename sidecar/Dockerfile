# Two-stage build: the first stage has the Go compiler, the second is a tiny image holding only the compiled program.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
# go.mod lists no dependencies on purpose: `go mod tidy` reads the imports in main.go and fetches the latest version of
# each (this also writes go.sum). That keeps the repo free of generated files, but means the dependency versions are
# whatever is latest when the image is built. To pin them, run `go mod tidy` once somewhere with Go installed and commit
# the resulting go.mod and go.sum, then drop this line.
RUN go mod tidy
# CGO_ENABLED=0 builds a fully static binary (the SQLite driver is pure Go, so no C library is needed).
RUN CGO_ENABLED=0 go build -trimpath -o /out/whatsapp-sidecar .

# distroless/static has no shell and no package manager; "nonroot" is its default user, the Deployment overrides it to
# uid 1000 so the program can read the files the WhatsApp server writes.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/whatsapp-sidecar /whatsapp-sidecar
ENTRYPOINT ["/whatsapp-sidecar"]
