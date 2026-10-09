#!/bin/sh
# Replace the binary of the Host: stop cumin after the current runs, install
# the new binary, restart, and check two polls for errors.
#
# Usage:
#   scripts/replace-binary.sh [--prefix <dir>] [--stop-timeout <seconds>]
#                             [--dry-run]
#
# The steps, in this order:
#   1. check the checkout: the default branch, no local change, equal to
#      the remote after a fetch;
#   2. check the target: the LaunchAgent is loaded, and runs <prefix>/cumin
#      (~/.local/bin by default), the path that the install writes;
#   3. cumin stop --after-current-runs, then wait until cumin has ended;
#   4. scripts/install.sh --prefix <prefix> --restart, which starts the
#      stopped job;
#   5. scripts/cumin-health.sh --wait-polls 2.
#
# The wait of step 3 ends with "time limit" after --stop-timeout seconds
# (3600 by default). cumin is then left as it is: it still stops after the
# current runs, and nothing is installed.
#
# With --dry-run, the script runs the checks that only read, prints the
# other steps, and changes nothing.
#
# The exit code is 0 when every step passes, 2 for a wrong option or a wrong
# value of an option, and 1 otherwise.
set -eu

label="dev.cloveclove.cumin"
prefix="${HOME}/.local/bin"
stop_timeout=3600
dry_run=0
# The tests set this: the seconds between two reads of the wait of step 3.
interval="${CUMIN_REPLACE_INTERVAL:-5}"

usage() {
  sed -n '2,27p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

die() {
  echo "error: $*" >&2
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) [ $# -ge 2 ] || usage; prefix="$2"; shift 2 ;;
    --stop-timeout) [ $# -ge 2 ] || usage; stop_timeout="$2"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
done

# A wrong value is a wrong call, like an unknown option: exit code 2.
bad_value() {
  echo "error: $*" >&2
  exit 2
}

[ -n "$prefix" ] || bad_value "--prefix needs a directory"
case "$stop_timeout" in
  ''|*[!0-9]*) bad_value "--stop-timeout needs a number of seconds" ;;
esac

for tool in git cumin launchctl plutil; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is not on PATH"
done

# The script works from any directory: the checkout is above this file.
root="$(cd "$(dirname "$0")/.." && pwd)"
plist="${HOME}/Library/LaunchAgents/${label}.plist"
target="gui/$(id -u)/$label"
# cumin stop writes this file, and cumin run removes it when that stop ends
# (docs/ja/designs/cumin-core.md, the topic on the stop).
stop_request="${HOME}/.local/state/cumin/stop-request.json"

# Step 1. The binary is built from this checkout, so the checkout must be
# what the remote holds on the default branch.
echo "step 1 of 5: check the checkout $root"
remote_head="$(git -C "$root" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null)" ||
  die "the default branch of origin is unknown. Set it with \"git remote set-head origin --auto\""
default_branch="${remote_head#origin/}"
branch="$(git -C "$root" symbolic-ref --short HEAD 2>/dev/null)" ||
  die "the checkout is on no branch. Switch to $default_branch"
[ "$branch" = "$default_branch" ] ||
  die "the checkout is on the branch $branch, not on the default branch $default_branch"
changes="$(git -C "$root" status --porcelain)" || die "cannot read the state of the checkout"
[ -z "$changes" ] || die "the checkout has a local change. Commit it elsewhere, or remove it"
if [ "$dry_run" -eq 1 ]; then
  echo "dry run: would run \"git fetch origin $default_branch\", and compare HEAD with origin/$default_branch"
else
  git -C "$root" fetch --quiet origin "$default_branch" || die "cannot fetch $default_branch from origin"
  local_commit="$(git -C "$root" rev-parse HEAD)" || die "cannot read the commit of the checkout"
  remote_commit="$(git -C "$root" rev-parse "origin/$default_branch")" || die "cannot read the commit of origin/$default_branch"
  [ "$local_commit" = "$remote_commit" ] ||
    die "the checkout is at $local_commit, and origin/$default_branch is at $remote_commit. Run \"git pull --ff-only\""
fi

# Step 2. The same check as scripts/install.sh --restart, before cumin
# stops: a wrong target must not leave cumin stopped.
echo "step 2 of 5: check the target $target"
launchctl print "$target" >/dev/null 2>&1 ||
  die "the LaunchAgent is not loaded, so there is nothing to replace. See docs/ja/development/setup-guide.md"
[ -f "$plist" ] || die "the job $label is loaded, but $plist is missing, so the binary that it runs cannot be checked. Write the plist again with \"cumin setup launchd\", then reload the job"
program="$(plutil -extract ProgramArguments.0 raw -o - "$plist")" ||
  die "cannot read the path of cumin from $plist"
if [ "$program" != "$prefix/cumin" ] && { [ ! -e "$program" ] || [ ! "$program" -ef "$prefix/cumin" ]; }; then
  die "the LaunchAgent runs $program, not $prefix/cumin. Install to that path (--prefix $(dirname "$program")), or write the plist again with \"cumin setup launchd --force\" and reload it"
fi

if [ "$dry_run" -eq 1 ]; then
  echo "dry run: step 3 of 5: would run \"cumin stop --after-current-runs\", and wait at most ${stop_timeout}s until cumin has ended"
  echo "dry run: step 4 of 5: would run \"$root/scripts/install.sh --prefix $prefix --restart\""
  echo "dry run: step 5 of 5: would run \"$root/scripts/cumin-health.sh --wait-polls 2\""
  echo "dry run: nothing changed"
  exit 0
fi

# Step 3. cumin run removes the stop request when it has ended, so the
# wait reads one local file. A cumin that does not run never removes the
# request: the wait then ends at the limit.
echo "step 3 of 5: stop cumin after the current runs"
cumin stop --after-current-runs || die "cumin stop failed. cumin is left as it is"
echo "waiting until cumin has ended, for at most ${stop_timeout}s"
started="$(date +%s)"
while [ -e "$stop_request" ]; do
  if [ $(($(date +%s) - started)) -ge "$stop_timeout" ]; then
    die "time limit: cumin has not ended in ${stop_timeout}s. Nothing was installed, and cumin is left as it is: it still stops after the current runs. Run this script again to wait longer. If cumin does not run, start it with \"scripts/install.sh --restart\""
  fi
  sleep "$interval"
done
echo "cumin has ended"

# Step 4. A clean stop leaves the job stopped, and the restart of the
# install starts it.
echo "step 4 of 5: install the new binary, and start cumin"
"$root/scripts/install.sh" --prefix "$prefix" --restart ||
  die "the install failed, and cumin is stopped. Correct the cause, then run \"scripts/install.sh --prefix $prefix --restart\""

echo "step 5 of 5: check two polls"
"$root/scripts/cumin-health.sh" --wait-polls 2 ||
  die "the new binary is installed and cumin was started, but the check of the polls failed. Read the errors above"
echo "replaced: $prefix/cumin runs, and two polls have no error"
