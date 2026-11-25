# Calyx core (Go). Build from the repository root.
#
# Production image (the default, last stage): the binaries on distroless with glibc.
#   docker build -f apps/core/Dockerfile -t calyx-core .
# Development image: the Go toolchain, runs the source that docker-compose.dev.yml mounts.
#   docker build -f apps/core/Dockerfile --target dev -t calyx-core:dev .
#
# The same image runs the core (default) and the webhook worker (command: worker).

FROM golang:1.27-trixie AS deps
WORKDIR /src
# Module files first, so the dependency download is cached between builds.
COPY go.work.sum ./
COPY packages/shared-types/go/go.mod packages/shared-types/go/go.sum packages/shared-types/go/
COPY apps/core/go.mod apps/core/go.sum apps/core/
# The root go.work lists modules that this image does not copy, so the image makes its own.
# go.work.sum stays, so the checksums are still checked.
RUN go work init ./apps/core ./packages/shared-types/go
RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/core && go mod download

FROM deps AS dev
WORKDIR /src/apps/core
ENV DATABASE_URL=file:/data/core.sqlite GRPC_PORT=50051 GRPC_HOST=0.0.0.0
EXPOSE 50051
CMD ["go", "run", "./cmd/core"]

# Reads the product version from the root package.json. A build argument can still override it.
FROM busybox:1.37 AS version
COPY package.json /package.json
RUN sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' /package.json | head -n 1 > /VERSION \
    && test -s /VERSION

FROM deps AS build
COPY packages/shared-types/go packages/shared-types/go
COPY apps/core apps/core
ARG VERSION=
COPY --from=version /VERSION /VERSION
# cgo is needed by the SQLite driver (github.com/mattn/go-sqlite3), so the binaries need glibc.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/core && CGO_ENABLED=1 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION:-$(cat /VERSION)}" \
    -o /out/ ./cmd/... \
    && mkdir /out/data

# No shell and no package manager: glibc, CA certificates, time zone data and the binaries.
FROM gcr.io/distroless/base-nossl-debian13 AS production
COPY --from=build /out/core /out/worker /usr/local/bin/
# Distroless has no mkdir or chown: copy an empty /data that the user 10001 owns.
COPY --from=build --chown=10001:10001 /out/data /data
USER 10001:10001
# Sessions and bulk jobs live here: mount a volume, or they are lost with the container.
VOLUME /data
ENV DATABASE_URL=file:/data/core.sqlite \
    LOG_FORMAT=json \
    GRPC_PORT=50051 \
    GRPC_HOST=0.0.0.0
EXPOSE 50051
CMD ["core"]
