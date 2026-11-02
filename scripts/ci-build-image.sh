#!/usr/bin/env bash
# Build the printpaca image for amd64 and arm64, gate its size, and on v*
# tags push a multi-arch image.
#
# The Dockerfile cross-compiles the Go binary per architecture, so neither
# build needs emulation. Docker login happens in the workflow. Runs locally
# too: it skips the push unless GITHUB_REF is a v* tag.
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/lib-tag.sh"

# OCI provenance labels (Dockerfile's final stage): REVISION is the full
# commit SHA; VERSION is the tag on a release and the short SHA otherwise.
# Outside CI they fall back to the Dockerfile defaults.
source_url=""
if [ -n "${GITHUB_SERVER_URL:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ]; then
  source_url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}"
fi

# Release tags report the tag (v1.0.0); every other build reports the short
# commit SHA. Two steps so VERSION degrades off-CI instead of tripping `set -u`.
case "${GITHUB_REF:-}" in
  refs/tags/v*) version="${GITHUB_REF_NAME:-${GITHUB_REF#refs/tags/}}" ;;
  *)
    version="${GITHUB_SHA:-}"
    version="${version:0:8}"
    ;;
esac

build_args=(
  --build-arg VERSION="${version:-dev}"
  --build-arg REVISION="${GITHUB_SHA:-unknown}"
  --build-arg SOURCE_URL="$source_url"
)

# One builder serves every build, so the multi-arch push reuses these layers.
for arch in amd64 arm64; do
  docker buildx build --platform "linux/$arch" --load \
    "${build_args[@]}" -t "printpaca:ci-$arch" .
done
# Gate each arch image: the rootfs differs by the Go binary.
for arch in amd64 arm64; do
  bash scripts/check-image-size.sh "printpaca:ci-$arch"
done

# Only v* tags publish; main, pull request, and manual runs stop here.
case "${GITHUB_REF:-}" in
  refs/tags/v*) ;;
  *)
    echo "not a v* tag (${GITHUB_REF:-unset}); skipping push"
    exit 0
    ;;
esac

# On a release tag a missing repository name is an error, not a skip.
: "${IMAGE_REPO:?IMAGE_REPO must be set (for example, skilledalpaca/printpaca)}"

tags=(-t "$IMAGE_REPO:$GITHUB_REF_NAME")
# :latest moves only for a strict stable release (vX.Y.Z), so a pre-release
# never reaches users who pull :latest.
if is_stable_release_tag "$GITHUB_REF_NAME"; then
  tags+=(-t "$IMAGE_REPO:latest")
else
  echo "$GITHUB_REF_NAME is a pre-release tag; not moving :latest"
fi

docker buildx build --platform linux/amd64,linux/arm64 --push \
  "${build_args[@]}" "${tags[@]}" .
