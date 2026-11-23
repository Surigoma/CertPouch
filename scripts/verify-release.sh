#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 || ! "$2" =~ ^[[:xdigit:]]{40}$ ]]; then
  echo "Usage: $0 ARTIFACT_DIRECTORY EXPECTED_COMMIT_SHA" >&2
  exit 2
fi

cd -- "$1"
expected_commit=$2
if ! sha256sum --binary certpouch-linux-amd64.tar.gz certpouch-linux-arm64.tar.gz |
    cmp -s - <(sed -E 's/^([a-f0-9]{64})  /\1 */' SHA256SUMS); then
  echo "SHA256SUMS does not match the two release archives" >&2
  exit 1
fi

scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
for arch in amd64 arm64; do
  name="certpouch-linux-$arch"
  binary="$scratch/$name"
  tar -xOzf "$name.tar.gz" "$name/certpouch" > "$binary"
  metadata=$(go version -m "$binary")
  for field in GOOS=linux "GOARCH=$arch" "vcs.revision=$expected_commit" vcs.modified=false; do
    if ! grep -Fxq $'\tbuild\t'"$field" <<< "$metadata"; then
      echo "$name is missing the expected build metadata: $field" >&2
      exit 1
    fi
  done
  if [[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && "$arch" == amd64 ]]; then
    chmod 0700 "$binary"
    "$binary" server --help > /dev/null 2>&1
  fi
  echo "$name verified"
done
