# Multi-arch: every build stage runs on the BUILD platform (no qemu) and only
# the Go binary is cross-compiled via TARGETARCH. The final scratch stage has
# no RUN steps, so a foreign-arch image never executes foreign-arch code.

# --- web build (vitest runs here too: fail the image on red tests) ---
# Pinned by digest so rebuilding a commit months apart yields the same image.
# To update: `docker buildx imagetools inspect node:26-alpine`, check the Node
# floor (CONTRIBUTING.md, package.json engines), replace the digest, and
# re-run the full test gate. Base refs are fully qualified (docker.io/library/)
# so resolution is identical on builders without unqualified-search registries.
FROM --platform=$BUILDPLATFORM docker.io/library/node:26-alpine@sha256:c6ad9e9c1d80f6eb607c26b4b0a132ff7a2344fad1c78ea3baa2a7d4c8de6d94 AS webbuild
WORKDIR /src
COPY package.json package-lock.json tsconfig.base.json vitest.config.ts ./
COPY web/package.json web/
RUN npm ci
COPY web web
# testdata/ holds the shared golden vectors that vitest reads.
COPY testdata testdata
RUN npx vitest run && npm run build -w web

# --- server build ---
# Pinned by digest because go.mod declares `go 1.26.10` as a floor and a
# floating tag would pick up whatever toolchain is latest. To update: `docker
# buildx imagetools inspect golang:1.26.10-alpine`, confirm it satisfies go.mod's
# floor, replace the digest, bump the tag when moving minors, and re-run the
# full test gate.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27.3-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS gobuild
WORKDIR /src
# Module and build caches live under /src so they can be chowned to the
# non-root build UID (golang:alpine defaults them under /root). Chowned once
# up front: a blanket `chown -R /src` after each COPY would duplicate the
# large module cache into a new layer on every source change.
ENV GOPATH=/src/.gopath GOCACHE=/src/.gocache
# 65532 (nonroot, distroless's convention) is this image's uid/gid and its
# source of truth; docker-compose.yml, packaging/unraid/README.md, and
# printpaca.xml point back here.
#
# /config-skel is an empty 65532-owned directory the final stage uses to seed
# VOLUME /config with the right ownership.
RUN mkdir -p "$GOPATH" "$GOCACHE" /config-skel && \
    chown -R 65532:65532 /src /config-skel
COPY --chown=65532:65532 server/go.mod server/go.sum ./
# Build and test as the final stage's non-root uid, so nothing needs root. The
# server tests avoid permission-bit tricks (chmod 0555) because root, which
# CI containers run as, bypasses them; see TestHandleHealthzConfigDirNotWritable
# in server/main_test.go.
USER 65532:65532
RUN go mod download
COPY --chown=65532:65532 server .
RUN rm -rf embedded/dist
COPY --from=webbuild --chown=65532:65532 /src/web/dist embedded/dist
# The server tests read ../testdata/money-format.json and
# ../config/config.schema.json relative to the package dir; place them at
# /testdata and /config.
COPY --chown=65532:65532 testdata /testdata
COPY --chown=65532:65532 config/config.schema.json /config/config.schema.json
# accent_theme_test.go checks the accent backgrounds against theme.css (../web/src/theme.css from /src).
COPY --from=webbuild --chown=65532:65532 /src/web/src/theme.css /web/src/theme.css
# Tests run on the BUILD platform in their own layer, so a multi-arch build
# runs them once and reuses the cached result per TARGETARCH.
RUN go test ./...
ARG VERSION=dev
# Declared this late on purpose: consuming TARGETARCH earlier would invalidate
# the shared layers above per architecture.
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /src/printpaca .

# --- final: scratch ---
FROM scratch
# OCI provenance labels, filled in by scripts/ci-build-image.sh; a plain local
# build uses the defaults below.
ARG VERSION=dev
ARG REVISION=unknown
ARG SOURCE_URL=""
LABEL org.opencontainers.image.title="printpaca" \
      org.opencontainers.image.description="Self-hosted 3D print cost and quote calculator" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.source="${SOURCE_URL}"
COPY --from=gobuild /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=gobuild /src/printpaca /printpaca
# Attribution for third-party code linked into the binary and web assets,
# kept next to the binary; update it when a runtime dependency changes
# (CONTRIBUTING.md).
COPY THIRD_PARTY_NOTICES.md /THIRD_PARTY_NOTICES.md
# Seed /config with a 65532-owned empty directory before VOLUME. An anonymous
# volume otherwise starts root:root, and since the container runs as 65532
# with no shell to chown it, main.go's writability probe fails and the
# container crash-loops. VOLUME copies the image's ownership at that path.
COPY --from=gobuild --chown=65532:65532 /config-skel /config
# Soft heap ceiling below the hard limit, so the Go GC works harder near the
# cap instead of the kernel OOM-killing the process (SIGKILL leaves no log).
# 112 MiB targets the 128m mem_limit in docker-compose.yml; keep it at about
# 85-90% of mem_limit, or unset it when running without a limit.
ENV GOMEMLIMIT=112MiB
USER 65532:65532
EXPOSE 8080
VOLUME /config
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/printpaca", "-healthcheck"]
ENTRYPOINT ["/printpaca"]
