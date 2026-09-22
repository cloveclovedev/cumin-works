#!/bin/sh
# Build cumin and put it where the Owner and launchd can run it.
#
# Usage:
#   scripts/install.sh [--prefix <dir>] [--restart]
#
# The default prefix is ~/.local/bin, a directory of the Owner's own user,
# so no sudo is needed. With --restart, the LaunchAgent is restarted when it
# is loaded, so that the new binary takes over. The LaunchAgent holds the
# path of cumin as it is, so it does not have to be written again.
set -eu

label="dev.cumin-works.cumin"
prefix="${HOME}/.local/bin"
restart=0

usage() {
  sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

die() {
  echo "error: $*" >&2
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) [ $# -ge 2 ] || usage; prefix="$2"; shift 2 ;;
    --restart) restart=1; shift ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
done

[ -n "$prefix" ] || die "--prefix needs a directory"
command -v go >/dev/null 2>&1 || die "go is not on PATH. See docs/ja/getting-started.md"

# The script works from any directory: the module is above this file.
root="$(cd "$(dirname "$0")/.." && pwd)"

# Build first, into a directory that is thrown away. A failed build then
# leaves the binary that is already installed in place.
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT INT TERM
(cd "$root" && go build -o "$staging/cumin" ./cmd/cumin) || die "the build failed. Nothing was installed"

mkdir -p "$prefix" || die "cannot create $prefix"
install -m 0755 "$staging/cumin" "$prefix/cumin" || die "cannot install into $prefix"
echo "installed: $prefix/cumin"

# A prefix that is not on PATH still works for launchd, because the plist
# holds the full path, but the Owner could not run "cumin" by name.
on_path=0
IFS=:
for dir in $PATH; do
  [ "${dir%/}" = "${prefix%/}" ] && on_path=1
done
unset IFS
if [ "$on_path" -eq 0 ]; then
  echo "warning: $prefix is not on PATH. Add it, or run $prefix/cumin by its path" >&2
fi

if [ "$restart" -eq 0 ]; then
  echo "the running cumin still uses the old binary. Restart it with: scripts/install.sh --restart, or launchctl kickstart -k gui/\$(id -u)/$label"
  exit 0
fi

command -v launchctl >/dev/null 2>&1 || die "launchctl is not on PATH: --restart works on macOS only"
target="gui/$(id -u)/$label"
if launchctl print "$target" >/dev/null 2>&1; then
  launchctl kickstart -k "$target" || die "cannot restart $target"
  echo "restarted: $target"
else
  echo "the LaunchAgent is not loaded, so there was nothing to restart. See docs/ja/development/setup-guide.md"
fi
