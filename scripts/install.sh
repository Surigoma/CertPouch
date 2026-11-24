#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: install.sh [--prefix PATH] [--replace] [BINARY]

Install the CertPouch Linux binary. BINARY defaults to the certpouch file next
to the release archive's install.sh. Configuration, credentials, state, and
systemd units are not modified.
EOF
}

prefix=/usr/local
replace=false
binary=
while (($#)); do
  case $1 in
    --prefix)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      prefix=$2
      shift 2
      ;;
    --replace)
      replace=true
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    --*)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      [[ -z $binary ]] || { usage >&2; exit 2; }
      binary=$1
      shift
      ;;
  esac
done

[[ $prefix == /* && $prefix != / && $prefix != *$'\n'* && $prefix != *$'\r'* ]] || {
  echo "--prefix must be an absolute path other than /" >&2
  exit 2
}

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
if [[ -z $binary ]]; then
  if [[ -f $script_dir/certpouch ]]; then
    binary=$script_dir/certpouch
  else
    binary=$script_dir/../certpouch
  fi
fi
[[ $(uname -s) == Linux ]] || { echo "CertPouch installation is supported only on Linux" >&2; exit 1; }
[[ -f $binary && ! -L $binary ]] || { echo "binary must be a regular file, not a symbolic link: $binary" >&2; exit 1; }
[[ -x $binary ]] || { echo "binary is not executable: $binary" >&2; exit 1; }

bin_dir=$prefix/bin
data_dir=$prefix/share/certpouch
destination=$bin_dir/certpouch
manifest=$data_dir/install-manifest
uninstaller=$data_dir/uninstall.sh

if [[ -e $destination && ! -f $manifest && $replace != true ]]; then
  echo "refusing to replace an unmanaged file: $destination (use --replace to override)" >&2
  exit 1
fi

install -d -m 0755 -- "$bin_dir" "$data_dir"
temp_binary=$(mktemp -- "$bin_dir/.certpouch.XXXXXXXX")
temp_manifest=$(mktemp -- "$data_dir/.install-manifest.XXXXXXXX")
temp_uninstaller=$(mktemp -- "$data_dir/.uninstall.XXXXXXXX")
cleanup() {
  rm -f -- "$temp_binary" "$temp_manifest" "$temp_uninstaller"
}
trap cleanup EXIT

install -m 0755 -- "$binary" "$temp_binary"
install -m 0755 -- "$script_dir/uninstall.sh" "$temp_uninstaller"
binary_hash=$(sha256sum --binary "$temp_binary" | awk '{print $1}')
printf 'format=1\nbinary=%s\nsha256=%s\nuninstaller=%s\n' \
  "$destination" "$binary_hash" "$uninstaller" > "$temp_manifest"
chmod 0644 "$temp_manifest"

mv -f -- "$temp_binary" "$destination"
mv -f -- "$temp_uninstaller" "$uninstaller"
mv -f -- "$temp_manifest" "$manifest"
trap - EXIT

echo "installed CertPouch to $destination"
echo "configuration and systemd units were not changed"
