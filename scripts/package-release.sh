#!/usr/bin/env bash
set -euo pipefail

output_dir=${1:-dist}
mkdir -p "$output_dir"
build_dir=$(mktemp -d)
trap 'rm -rf -- "$build_dir"' EXIT
archives=()

for arch in amd64 arm64; do
  name="certpouch-linux-$arch"
  mkdir "$build_dir/$name"
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -o "$build_dir/$name/certpouch" ./cmd/certpouch
  git archive HEAD LICENSE README.md README.ja.md assets configs docs | tar -xf - -C "$build_dir/$name"
  tar -czf "$output_dir/$name.tar.gz" -C "$build_dir" "$name"
  archives+=("$name.tar.gz")
done

(cd "$output_dir" && sha256sum "${archives[@]}" > SHA256SUMS)
