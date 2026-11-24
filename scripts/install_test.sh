#!/usr/bin/env bash
set -euo pipefail

repo_root=${CERTPOUCH_TEST_REPO_ROOT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)}
script_dir=${CERTPOUCH_TEST_SCRIPT_DIR:-$repo_root/scripts}
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
prefix=$scratch/usr/local
fake_binary=$scratch/certpouch
printf '#!/usr/bin/env bash\necho certpouch-test\n' > "$fake_binary"
chmod 0755 "$fake_binary"

bash "$script_dir/install.sh" --prefix "$prefix" "$fake_binary"
[[ $($prefix/bin/certpouch) == certpouch-test ]]
[[ -x $prefix/share/certpouch/uninstall.sh ]]
[[ -f $prefix/share/certpouch/install-manifest ]]

printf '# modified\n' >> "$prefix/bin/certpouch"
if "$prefix/share/certpouch/uninstall.sh" --prefix "$prefix" >/dev/null 2>&1; then
  echo "uninstaller removed a modified binary without --force" >&2
  exit 1
fi
[[ -f $prefix/bin/certpouch ]]
"$prefix/share/certpouch/uninstall.sh" --prefix "$prefix" --force
[[ ! -e $prefix/bin/certpouch ]]
[[ ! -e $prefix/share/certpouch/install-manifest ]]

unmanaged=$scratch/unmanaged/bin/certpouch
mkdir -p "$(dirname -- "$unmanaged")"
printf 'unmanaged\n' > "$unmanaged"
if bash "$script_dir/install.sh" --prefix "$scratch/unmanaged" "$fake_binary" >/dev/null 2>&1; then
  echo "installer replaced an unmanaged binary without --replace" >&2
  exit 1
fi
grep -Fxq unmanaged "$unmanaged"

echo "install and uninstall tests passed"
