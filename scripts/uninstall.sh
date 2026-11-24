#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: uninstall.sh [--prefix PATH] [--force]

Remove a CertPouch binary installed by install.sh. Configuration, credentials,
certificates, state, service accounts, and systemd units are always preserved.
EOF
}

prefix=/usr/local
force=false
while (($#)); do
  case $1 in
    --prefix)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      prefix=$2
      shift 2
      ;;
    --force)
      force=true
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ $prefix == /* && $prefix != / && $prefix != *$'\n'* && $prefix != *$'\r'* ]] || {
  echo "--prefix must be an absolute path other than /" >&2
  exit 2
}
[[ $(uname -s) == Linux ]] || { echo "CertPouch uninstallation is supported only on Linux" >&2; exit 1; }

data_dir=$prefix/share/certpouch
manifest=$data_dir/install-manifest
[[ -f $manifest && ! -L $manifest ]] || {
  echo "CertPouch install manifest not found: $manifest" >&2
  exit 1
}

format=$(sed -n 's/^format=//p' "$manifest")
binary=$(sed -n 's/^binary=//p' "$manifest")
expected_hash=$(sed -n 's/^sha256=//p' "$manifest")
uninstaller=$(sed -n 's/^uninstaller=//p' "$manifest")
expected_binary=$prefix/bin/certpouch
expected_uninstaller=$data_dir/uninstall.sh
if [[ $format != 1 || $binary != "$expected_binary" || $uninstaller != "$expected_uninstaller" ||
      ! $expected_hash =~ ^[a-f0-9]{64}$ ]]; then
  echo "invalid CertPouch install manifest: $manifest" >&2
  exit 1
fi

if [[ -e $binary ]]; then
  [[ -f $binary && ! -L $binary ]] || {
    echo "refusing to remove a non-regular installed path: $binary" >&2
    exit 1
  }
  actual_hash=$(sha256sum --binary "$binary" | awk '{print $1}')
  if [[ $actual_hash != "$expected_hash" && $force != true ]]; then
    echo "installed binary has been modified; refusing to remove it (use --force to override)" >&2
    exit 1
  fi
  rm -f -- "$binary"
fi

rm -f -- "$manifest"
rm -f -- "$uninstaller"
rmdir -- "$data_dir" 2>/dev/null || true

echo "removed CertPouch from $binary"
echo "configuration, credentials, certificates, state, and systemd units were preserved"
