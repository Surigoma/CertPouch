#!/usr/bin/env bash
set -euo pipefail

output_dir=${1:-dist}
mkdir -p "$output_dir"
build_dir=$(mktemp -d)
trap 'rm -rf -- "$build_dir"' EXIT
staged_output="$build_dir/release"
mkdir "$staged_output"
archives=()

for arch in amd64 arm64; do
  name="certpouch-linux-$arch"
  mkdir "$build_dir/$name"
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -o "$build_dir/$name/certpouch" ./cmd/certpouch
  git archive HEAD LICENSE README.md README.ja.md assets configs docs | tar -xf - -C "$build_dir/$name"
  tar -czf "$staged_output/$name.tar.gz" -C "$build_dir" "$name"
  archives+=("$name.tar.gz")
done

(cd "$staged_output" && sha256sum "${archives[@]}" > SHA256SUMS)
for archive in "${archives[@]}"; do
  cp "$staged_output/$archive" "$output_dir/"
done
cp "$staged_output/SHA256SUMS" "$output_dir/"
