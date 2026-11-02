# Shared "is this tag a stable release" check, sourced by
# ci-build-image.sh to decide whether :latest moves.
#
# The workflow's `tags: ["v*"]` trigger and its startsWith(...) guard are a
# separate, looser check (workflow YAML can't source a bash file): they
# only gate "is this a v* tag at all", not "is it stable". This regex is
# the authority for the stricter, stable-vs-prerelease distinction.

# is_stable_release_tag TAG
# Returns success (0) for a strict stable release (vX.Y.Z, no pre-release
# suffix like -rc1/-beta.1), failure otherwise.
is_stable_release_tag() {
  [[ "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
}
