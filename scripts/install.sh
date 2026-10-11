#!/bin/sh
# Build cumin and put it where the Operator and launchd can run it.
#
# Usage:
#   scripts/install.sh [--prefix <dir>] [--restart]
#   scripts/install.sh [--prefix <dir>] --after-current-runs
#                      [--stop-timeout <seconds>]
#
# The default prefix is ~/.local/bin, a directory of the Operator's own user,
# so no sudo is needed. With --restart, the LaunchAgent is restarted when it
# is loaded, so that the new binary takes over. The LaunchAgent holds the
# path of cumin as it is, so it does not have to be written again.
#
# --restart ends the agents that run. With --after-current-runs, the script
# replaces the binary without that. The steps, in this order:
#   1. check the checkout: the default branch, no local change, equal to
#      the remote after a fetch;
#   2. check the LaunchAgent: it is loaded, and runs <prefix>/cumin;
#   3. build into the staging directory;
#   4. cumin stop --after-current-runs, then wait until cumin has ended;
#   5. install, and restart the LaunchAgent;
#   6. scripts/cumin-health.sh --wait-polls 2.
#
# The wait of step 4 ends with "time limit" after --stop-timeout seconds
# (3600 by default). cumin is then left as it is: it still stops after the
# current runs, and nothing is installed.
#
# The exit code is 0 when every step passes, 2 for a wrong option or a wrong
# value of --stop-timeout, and 1 otherwise.
set -eu

label="dev.cloveclove.cumin"
prefix="${HOME}/.local/bin"
restart=0
after_runs=0
stop_timeout=3600
# The tests set this: the seconds between two reads of the wait of step 4.
interval="${CUMIN_INSTALL_INTERVAL:-5}"
# After step 4, cumin is stopped, and a failure says so.
stopped=""

usage() {
  sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'
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
    --after-current-runs) after_runs=1; shift ;;
    --stop-timeout) [ $# -ge 2 ] || usage; stop_timeout="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
done

# A wrong value is a wrong call, like an unknown option: exit code 2.
case "$stop_timeout" in
  ''|*[!0-9]*) echo "error: --stop-timeout needs a number of seconds" >&2; exit 2 ;;
esac

[ -n "$prefix" ] || die "--prefix needs a directory"
command -v go >/dev/null 2>&1 || die "go is not on PATH. See docs/ja/getting-started.md"

# The script works from any directory: the module is above this file.
root="$(cd "$(dirname "$0")/.." && pwd)"
plist="${HOME}/Library/LaunchAgents/${label}.plist"

# A restart starts the path that the job already holds, so a job that runs
# another binary would come back on the old one while this script reported
# success. The path is read from the plist, not from "launchctl print",
# whose output is not an interface (man launchctl).
check_program() {
  [ -f "$plist" ] || die "the job $label is loaded, but $plist is missing, so the binary that it runs cannot be checked. Write the plist again with \"cumin setup launchd\", then reload the job"
  program="$(plutil -extract ProgramArguments.0 raw -o - "$plist")" ||
    die "cannot read the path of cumin from $plist"
  # The plist may hold a symbolic link that points at the installed file, so
  # compare the files as well as the paths.
  if [ "$program" != "$prefix/cumin" ] && { [ ! -e "$program" ] || [ ! "$program" -ef "$prefix/cumin" ]; }; then
    die "the LaunchAgent runs $program, not $prefix/cumin. Install to that path (--prefix $(dirname "$program")), or write the plist again with \"cumin setup launchd --force\" and reload it"
  fi
}

if [ "$after_runs" -eq 1 ]; then
  for tool in git cumin launchctl plutil; do
    command -v "$tool" >/dev/null 2>&1 || die "$tool is not on PATH"
  done
  target="gui/$(id -u)/$label"
  # cumin stop writes this file, and cumin run removes it when that stop ends
  # (docs/ja/designs/cumin-core.md, the topic on the stop).
  stop_request="${HOME}/.local/state/cumin/stop-request.json"

  # Step 1. The binary is built from this checkout, so the checkout must be
  # what the remote holds on the default branch.
  echo "step 1 of 6: check the checkout $root"
  remote_head="$(git -C "$root" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null)" ||
    die "the default branch of origin is unknown. Set it with \"git remote set-head origin --auto\""
  default_branch="${remote_head#origin/}"
  branch="$(git -C "$root" symbolic-ref --short HEAD 2>/dev/null)" ||
    die "the checkout is on no branch. Switch to $default_branch"
  [ "$branch" = "$default_branch" ] ||
    die "the checkout is on the branch $branch, not on the default branch $default_branch"
  changes="$(git -C "$root" status --porcelain)" || die "cannot read the state of the checkout"
  [ -z "$changes" ] || die "the checkout has a local change. Commit it elsewhere, or remove it"
  git -C "$root" fetch --quiet origin "$default_branch" || die "cannot fetch $default_branch from origin"
  local_commit="$(git -C "$root" rev-parse HEAD)" || die "cannot read the commit of the checkout"
  remote_commit="$(git -C "$root" rev-parse "origin/$default_branch")" || die "cannot read the commit of origin/$default_branch"
  [ "$local_commit" = "$remote_commit" ] ||
    die "the checkout is at $local_commit, and origin/$default_branch is at $remote_commit. Run \"git pull --ff-only\""

  # Step 2. Before cumin stops: a wrong LaunchAgent must not leave cumin
  # stopped.
  echo "step 2 of 6: check the LaunchAgent $target"
  launchctl print "$target" >/dev/null 2>&1 ||
    die "the LaunchAgent is not loaded, so there is nothing to replace. See docs/ja/development/setup-guide.md"
  check_program
  echo "step 3 of 6: build"
fi

# Build first, into a directory that is thrown away. A failed build then
# leaves the binary that is already installed in place.
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT
# A signal ends the script: the wait of step 4 must not continue without
# the staging directory.
trap 'exit 1' INT TERM
(cd "$root" && go build -o "$staging/cumin" ./cmd/cumin) || die "the build failed. Nothing was installed"

if [ "$after_runs" -eq 1 ]; then
  # Step 4. cumin run removes the stop request when it has ended, so the
  # wait reads one local file. A cumin that does not run never removes the
  # request: the wait then ends at the limit.
  echo "step 4 of 6: stop cumin after the current runs"
  cumin stop --after-current-runs || die "cumin stop failed. Nothing was installed, and cumin is left as it is"
  echo "waiting until cumin has ended, for at most ${stop_timeout}s"
  started="$(date +%s)"
  while [ -e "$stop_request" ]; do
    if [ $(($(date +%s) - started)) -ge "$stop_timeout" ]; then
      die "time limit: cumin has not ended in ${stop_timeout}s. Nothing was installed, and cumin is left as it is: it still stops after the current runs. Run this script again to wait longer. If cumin does not run, start it with \"scripts/install.sh --restart\""
    fi
    sleep "$interval"
  done
  echo "cumin has ended"
  stopped=", and cumin is stopped. Correct the cause, then run \"scripts/install.sh --prefix $prefix --restart\""
  # Step 5. A clean stop leaves the job stopped, and the restart starts it.
  echo "step 5 of 6: install the new binary, and start cumin"
fi

mkdir -p "$prefix" || die "cannot create $prefix$stopped"
install -m 0755 "$staging/cumin" "$prefix/cumin" || die "cannot install into $prefix$stopped"
echo "installed: $prefix/cumin"

# A prefix that is not on PATH still works for launchd, because the plist
# holds the full path, but the Operator could not run "cumin" by name.
on_path=0
IFS=:
for dir in $PATH; do
  [ "${dir%/}" = "${prefix%/}" ] && on_path=1
done
unset IFS
if [ "$on_path" -eq 0 ]; then
  echo "warning: $prefix is not on PATH. Add it, or run $prefix/cumin by its path" >&2
fi

if [ "$after_runs" -eq 1 ]; then
  launchctl kickstart -k "$target" || die "cannot restart $target$stopped"
  echo "restarted: $target ($program)"

  echo "step 6 of 6: check two polls"
  "$root/scripts/cumin-health.sh" --wait-polls 2 ||
    die "the new binary is installed and cumin was started, but the check of the polls failed. Read the errors above"
  echo "replaced: $prefix/cumin runs, and two polls have no error"
  exit 0
fi

if [ "$restart" -eq 0 ]; then
  echo "the running cumin still uses the old binary. Restart it with: scripts/install.sh --restart, or launchctl kickstart -k gui/\$(id -u)/$label"
  exit 0
fi

command -v launchctl >/dev/null 2>&1 || die "launchctl is not on PATH: --restart works on macOS only"
command -v plutil >/dev/null 2>&1 || die "plutil is not on PATH: --restart works on macOS only"

target="gui/$(id -u)/$label"

# A Host where cumin was never set up has nothing to restart. Say so and
# finish: the binary is installed, which is what the caller asked for.
if ! launchctl print "$target" >/dev/null 2>&1; then
  echo "the LaunchAgent is not loaded, so there was nothing to restart. Load it with \"launchctl bootstrap gui/\$(id -u) $plist\". See docs/ja/development/setup-guide.md"
  exit 0
fi

check_program

launchctl kickstart -k "$target" || die "cannot restart $target"
echo "restarted: $target ($program)"
