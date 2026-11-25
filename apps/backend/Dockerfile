# Calyx backend. Build from the repository root.
#
# Production image (the default, last stage): one static binary on distroless, about 15 MB.
#   docker build -f apps/backend/Dockerfile -t calyx-backend .
# Development image: the Go toolchain, runs the source that docker-compose.dev.yml mounts.
#   docker build -f apps/backend/Dockerfile --target dev -t calyx-backend:dev .
#
# The image runs "backend serve" by default; "backend migrate" and the operator commands use the
# same image and the same environment.

FROM golang:1.27-trixie AS deps
WORKDIR /src
# Module files first, so the dependency download is cached between builds.
COPY go.work.sum ./
COPY packages/shared-types/go/go.mod packages/shared-types/go/go.sum packages/shared-types/go/
COPY apps/backend/go.mod apps/backend/go.sum apps/backend/
# The root go.work lists modules that this image does not copy, so the image makes its own.
# go.work.sum stays, so the checksums are still checked.
RUN go work init ./apps/backend ./packages/shared-types/go
RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/backend && go mod download

FROM deps AS dev
WORKDIR /src/apps/backend
ENV HTTP_HOST=0.0.0.0 HTTP_PORT=8080
EXPOSE 8080
CMD ["go", "run", "./cmd/backend", "serve"]

# Reads the product version from the root package.json. A build argument can still override it.
FROM busybox:1.37 AS version
COPY package.json /package.json
RUN sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' /package.json | head -n 1 > /VERSION \
    && test -s /VERSION

FROM deps AS build
COPY packages/shared-types/go packages/shared-types/go
COPY apps/backend apps/backend
ARG VERSION=
COPY --from=version /VERSION /VERSION
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/backend && CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION:-$(cat /VERSION)}" \
    -o /out/backend ./cmd/backend

# No shell and no package manager: only the binary, CA certificates and time zone data.
FROM gcr.io/distroless/static-debian13 AS production
COPY --from=build /out/backend /usr/local/bin/backend
USER 10001:10001
ENV HTTP_HOST=0.0.0.0 \
    HTTP_PORT=8080 \
    LOG_FORMAT=json
EXPOSE 8080
ENTRYPOINT ["backend"]
CMD ["serve"]
