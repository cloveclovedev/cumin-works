#!/bin/sh
# Run the live scenario E2E-1 on the sandbox with the cumin of the Host,
# and put the Host back on its repositories whatever the result is.
#
# Usage:
#   scripts/live-scenario.sh --repo <owner>/<sandbox>
#                            --config <Host settings file for the sandbox>
#                            [--stop-timeout <seconds>]
#                            [--test-timeout <seconds>]
#
# The steps, in this order:
#   1. check that the sandbox is clean: no open issue with a status label
#      of work in progress (docs/ja/development/live-tests.md). A failed
#      read is a failure, not "clean";
#   2. cumin stop --after-current-runs, then wait until cumin has ended;
#   3. keep a copy of the Host settings file (the file after --config in
#      the plist of the LaunchAgent), put the file of --config in its
#      place, and start cumin;
#   4. run TestLiveE2E with CUMIN_LIVE=1 and CUMIN_LIVE_REPO;
#   5. put the Host settings file back, start cumin again, and run
#      scripts/cumin-health.sh --wait-polls 2.
#
# Step 5 runs when step 3 or step 4 fails, and on a signal (INT, TERM,
# HUP), too. The copy of step 3 is "<Host settings file>.before-live-scenario".
#
# The wait of step 2 ends with "time limit" after --stop-timeout seconds
# (3600 by default). Nothing is changed then. The test of step 4 is stopped
# with "time limit" after --test-timeout seconds (14400 by default).
#
# The last two lines are the result of the test ("test: ...") and the
# result of step 5 ("host: ...").
#
# The exit code is 0 when the test passes and step 5 passes, 2 for a wrong
# option or a wrong value of an option, and 1 otherwise.
set -eu

label="dev.cloveclove.cumin"
repo=""
sandbox_config=""
stop_timeout=3600
test_timeout=14400
# The tests set this: the seconds between two reads of a wait.
interval="${CUMIN_LIVE_SCENARIO_INTERVAL:-5}"
# An open issue with one of these labels stops TestLiveE2E at its first
# check, or takes the slot of the issues in work.
work_labels="cumin/status/ready cumin/status/planning cumin/status/implementing cumin/status/checking cumin/status/reviewing"

usage() {
  sed -n '2,34p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

die() {
  echo "error: $*" >&2
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --repo) [ $# -ge 2 ] || usage; repo="$2"; shift 2 ;;
    --config) [ $# -ge 2 ] || usage; sandbox_config="$2"; shift 2 ;;
    --stop-timeout) [ $# -ge 2 ] || usage; stop_timeout="$2"; shift 2 ;;
    --test-timeout) [ $# -ge 2 ] || usage; test_timeout="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
done

# A wrong value is a wrong call, like an unknown option: exit code 2.
bad_value() {
  echo "error: $*" >&2
  exit 2
}

case "$repo" in
  */*/*|/*|*/|*' '*) bad_value "--repo needs <owner>/<repository>" ;;
  */*) ;;
  *) bad_value "--repo needs <owner>/<repository>" ;;
esac
[ -n "$sandbox_config" ] || bad_value "--config needs the Host settings file for the sandbox"
case "$stop_timeout" in
  ''|*[!0-9]*) bad_value "--stop-timeout needs a number of seconds" ;;
esac
case "$test_timeout" in
  ''|*[!0-9]*) bad_value "--test-timeout needs a number of seconds" ;;
esac

for tool in gh go cumin launchctl plutil; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is not on PATH"
done

# The script works from any directory: the checkout is above this file.
root="$(cd "$(dirname "$0")/.." && pwd)"
plist="${HOME}/Library/LaunchAgents/${label}.plist"
target="gui/$(id -u)/$label"
# cumin stop writes this file, and cumin run removes it when that stop ends
# (docs/ja/designs/cumin-core.md, the topic on the stop).
stop_request="${HOME}/.local/state/cumin/stop-request.json"

# Step 1. Everything here only reads, and runs before cumin stops: a
# sandbox that is not clean must not leave the Host stopped or switched.
echo "step 1 of 5: check that the sandbox $repo is clean"
for work_label in $work_labels; do
  open="$(gh issue list --repo "$repo" --state open --label "$work_label" --json number --jq '.[].number')" ||
    die "cannot read the open issues of $repo with the label $work_label. A failed read is not \"clean\". Nothing was changed"
  [ -z "$open" ] ||
    die "the sandbox $repo is not clean: an open issue has the label $work_label (#$(echo $open | sed 's/ / #/g')). Close it, or remove the label. Nothing was changed"
done

[ -f "$sandbox_config" ] || die "the settings file for the sandbox $sandbox_config is missing"
launchctl print "$target" >/dev/null 2>&1 ||
  die "the LaunchAgent is not loaded, so there is no cumin to run the scenario. See docs/ja/development/setup-guide.md"
[ -f "$plist" ] || die "the job $label is loaded, but $plist is missing, so the Host settings file is unknown. Write the plist again with \"cumin setup launchd\", then reload the job"
# The Host settings file is the argument after --config in the plist: the
# file that the cumin of the LaunchAgent reads.
host_config=""
index=0
while argument="$(plutil -extract "ProgramArguments.$index" raw -o - "$plist" 2>/dev/null)"; do
  index=$((index + 1))
  if [ "$argument" = "--config" ]; then
    host_config="$(plutil -extract "ProgramArguments.$index" raw -o - "$plist" 2>/dev/null)" || host_config=""
    break
  fi
done
[ -n "$host_config" ] || die "the plist $plist has no --config argument, so the Host settings file is unknown. Write the plist again with \"cumin setup launchd --force\", then reload the job"
[ -f "$host_config" ] || die "the Host settings file $host_config is missing"
[ ! "$sandbox_config" -ef "$host_config" ] ||
  die "--config names the Host settings file itself. Give a separate file with the settings of the sandbox"
backup="$host_config.before-live-scenario"
[ ! -e "$backup" ] ||
  die "$backup exists: an earlier run did not put the Host settings back. Compare it with $host_config, move it back if it is the Host settings, then run this script again"

# Step 2. cumin run removes the stop request when it has ended, so the
# wait reads one local file. A cumin that does not run never removes the
# request: the wait then ends at the limit.
echo "step 2 of 5: stop cumin after the current runs"
cumin stop --after-current-runs || die "cumin stop failed. cumin is left as it is, and the Host settings are not changed"
echo "waiting until cumin has ended, for at most ${stop_timeout}s"
started="$(date +%s)"
while [ -e "$stop_request" ]; do
  if [ $(($(date +%s) - started)) -ge "$stop_timeout" ]; then
    die "time limit: cumin has not ended in ${stop_timeout}s. The Host settings are not changed, and cumin is left as it is: it still stops after the current runs. Run this script again to wait longer. If cumin does not run, start it with \"launchctl kickstart gui/\$(id -u)/$label\""
  fi
  sleep "$interval"
done
echo "cumin has ended"

test_result="not run"
host_result=""
test_pid=""
exit_code=1

# put_back is step 5. It sets host_result to "" when the Host is back.
put_back() {
  echo "step 5 of 5: put the Host settings back, start cumin again, and check two polls"
  # The copy exists from the moment the Host settings can differ. A rename
  # puts all of the file back, or nothing.
  if [ -e "$backup" ] && ! mv -f "$backup" "$host_config"; then
    host_result="failed: cannot put the Host settings back. cumin still has the settings of the sandbox. Move $backup to $host_config, then run \"launchctl kickstart -k $target\""
    return
  fi
  # The cumin that runs holds the settings of the sandbox, so it is
  # replaced at once. Only a run on the sandbox can end with it.
  if ! launchctl kickstart -k "$target"; then
    host_result="failed: the Host settings are back, but cumin did not start. Run \"launchctl kickstart -k $target\""
    return
  fi
  if ! "$root/scripts/cumin-health.sh" --wait-polls 2; then
    host_result="failed: the Host settings are back and cumin was started, but the check of the polls failed. Read the errors above"
    return
  fi
  host_result=""
}

# finish runs at every end after step 2: step 5, then the two results.
finish() {
  trap '' INT TERM HUP
  trap - EXIT
  put_back
  echo "test: $test_result"
  if [ -z "$host_result" ]; then
    echo "host: the Host settings are back, cumin runs, and two polls have no error"
    [ "$test_result" != "passed" ] || exit_code=0
  else
    echo "host: $host_result"
    exit_code=1
  fi
  exit "$exit_code"
}

# stop_test ends the test and everything that it started: the test has its
# own process group.
stop_test() {
  [ -n "$test_pid" ] || return 0
  kill -TERM -- "-$test_pid" 2>/dev/null || true
  wait "$test_pid" 2>/dev/null || true
  test_pid=""
}

on_signal() {
  trap '' INT TERM HUP
  echo "signal: stop the scenario, and put the Host back"
  if [ -n "$test_pid" ]; then
    test_result="stopped by a signal"
    stop_test
  fi
  exit 1
}

trap finish EXIT
trap on_signal INT TERM HUP

# Step 3. From here, every end goes through step 5.
echo "step 3 of 5: put the settings of the sandbox in place of $host_config, and start cumin"
cp -p "$host_config" "$backup.tmp" && mv -f "$backup.tmp" "$backup" ||
  die "cannot keep a copy of the Host settings at $backup"
cp "$sandbox_config" "$host_config" || die "cannot put the settings of the sandbox in place of $host_config"
launchctl kickstart "$target" || die "cannot start $target with the settings of the sandbox"

# Step 4. The limit is the one of this script, so the test has none of its
# own. The test runs in its own process group (set -m), so that the limit
# and a signal end the test binary too, not only the go command.
echo "step 4 of 5: run TestLiveE2E on $repo, for at most ${test_timeout}s"
set -m
(cd "$root" && CUMIN_LIVE=1 CUMIN_LIVE_REPO="$repo" exec go test -count=1 -timeout 0 -run TestLiveE2E -v ./cmd/cumin/) &
test_pid=$!
set +m
started="$(date +%s)"
while kill -0 "$test_pid" 2>/dev/null; do
  if [ $(($(date +%s) - started)) -ge "$test_timeout" ]; then
    test_result="time limit: the test has not ended in ${test_timeout}s, and the script stopped it"
    stop_test
    exit 1
  fi
  sleep "$interval"
done
test_exit=0
wait "$test_pid" || test_exit=$?
test_pid=""
if [ "$test_exit" -eq 0 ]; then
  test_result="passed"
else
  test_result="failed (exit code $test_exit)"
fi
exit 1
