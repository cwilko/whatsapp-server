# Two-stage build: the first stage has the Go compiler, the second is a tiny image holding only the compiled program.
FROM golang:1.26-alpine AS build
WORKDIR /src
# go.mod and go.sum pin every dependency version (and checksum), so a rebuild gets the same code. The builder image's Go
# must be at least the `go` line in go.mod: the official golang image uses GOTOOLCHAIN=local, so it refuses to build
# otherwise. To update the dependencies, run `go get -u ./... && go mod tidy` in sidecar/ and commit go.mod and go.sum.
COPY go.mod go.sum main.go ./
# -mod=readonly (below) makes the build fail rather than change go.mod or go.sum.
RUN go mod download
# CGO_ENABLED=0 builds a fully static binary (the SQLite driver is pure Go, so no C library is needed).
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/whatsapp-sidecar .

# distroless/static has no shell and no package manager; "nonroot" is its default user, the Deployment overrides it to
# uid 1000 so the program can read the files the WhatsApp server writes.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/whatsapp-sidecar /whatsapp-sidecar
ENTRYPOINT ["/whatsapp-sidecar"]
