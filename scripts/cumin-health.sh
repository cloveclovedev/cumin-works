#!/bin/sh
# Say whether cumin is running: the last poll, the errors since the start,
# and the agents at work.
#
# Usage:
#   scripts/cumin-health.sh [--stale-after <seconds>]
#                           [--wait-polls <n> [--timeout <seconds>]]
#                           [--monitor <file>] [--plist <file>]
#
# The script reads the monitor file that cumin run writes after every poll
# (~/.local/state/cumin/monitor.json), and the log that the plist of the
# LaunchAgent names. It reads no setting of cumin and does not ask GitHub.
#
# The exit code is 0 when the last poll is fresh and has no error, and 1
# otherwise. The last poll is fresh when it is at most --stale-after seconds
# old (180 by default). A monitor file that is missing or unreadable is a
# failure. The errors since the start are shown only: they do not change
# the exit code.
#
# With --wait-polls, the script first waits until the time of the last poll
# moves n times. It fails at once when one of these polls has an error, and
# it fails with "time limit" when --timeout seconds (300 by default) pass.
set -eu

label="dev.cloveclove.cumin"
monitor="${HOME}/.local/state/cumin/monitor.json"
plist="${HOME}/Library/LaunchAgents/${label}.plist"
stale_after=180
wait_polls=0
timeout=300
# The tests set these two: a fixed "now" (the form of last_poll.at) for the
# age of the last poll, and the seconds between two reads of the wait.
fixed_now="${CUMIN_HEALTH_NOW:-}"
interval="${CUMIN_HEALTH_INTERVAL:-2}"
# The newest version of the monitor file that this script knows.
known_version=1

usage() {
  sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

die() {
  echo "error: $*" >&2
  exit 1
}

is_number() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
  esac
}

while [ $# -gt 0 ]; do
  case "$1" in
    --stale-after) [ $# -ge 2 ] || usage; stale_after="$2"; shift 2 ;;
    --wait-polls) [ $# -ge 2 ] || usage; wait_polls="$2"; shift 2 ;;
    --timeout) [ $# -ge 2 ] || usage; timeout="$2"; shift 2 ;;
    --monitor) [ $# -ge 2 ] || usage; monitor="$2"; shift 2 ;;
    --plist) [ $# -ge 2 ] || usage; plist="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
done

is_number "$stale_after" || die "--stale-after needs a number of seconds"
is_number "$wait_polls" || die "--wait-polls needs a number of polls"
is_number "$timeout" || die "--timeout needs a number of seconds"
command -v plutil >/dev/null 2>&1 || die "plutil is not on PATH: this script works on macOS only"

# plutil reads JSON as well as a plist (man plutil), so no other tool is
# needed. An array gives its length.
field() {
  plutil -extract "$1" raw -o - "$monitor" 2>/dev/null
}

# The seconds since the epoch of a time in the form of last_poll.at
# (RFC 3339, UTC), with or without a fraction of a second.
epoch() {
  date -j -u -f '%Y-%m-%dT%H:%M:%SZ' "$(printf '%s' "$1" | sed 's/\.[0-9]*Z$/Z/')" +%s 2>/dev/null
}

# Stop when the monitor file cannot say anything about the last poll.
check_monitor() {
  [ -f "$monitor" ] || die "the monitor file $monitor is missing. cumin run writes it after its first poll, so cumin did not run on this Host, or runs with another home directory"
  [ -r "$monitor" ] || die "the monitor file $monitor is unreadable"
  version="$(field version)" || die "the monitor file $monitor is unreadable: it is not the JSON that cumin run writes"
  is_number "$version" || die "the monitor file $monitor is unreadable: its version is not a number"
  [ "$version" -le "$known_version" ] || die "the monitor file $monitor has version $version, and this script knows version $known_version. Use the script of the cumin that runs"
  field last_poll.at >/dev/null || die "the monitor file $monitor is unreadable: it has no last_poll.at"
}

# The errors since the start: the lines of level ERROR in the log of the
# LaunchAgent, after the last line "cumin run starts". Only the time and
# the message of a line are printed, never its other fields.
print_log_errors() {
  if [ ! -f "$plist" ]; then
    echo "errors since the start: unknown (the plist $plist is missing, so the log has no known path)"
    return 0
  fi
  if ! log="$(plutil -extract StandardOutPath raw -o - "$plist" 2>/dev/null)"; then
    echo "errors since the start: unknown (cannot read StandardOutPath from $plist)"
    return 0
  fi
  if [ ! -r "$log" ]; then
    echo "errors since the start: unknown (cannot read the log $log)"
    return 0
  fi
  awk '
    /"msg":"cumin run starts"/ { started = 1; n = 0; next }
    /"level":"ERROR"/ { lines[++n] = $0 }
    END {
      if (!started) { print "none"; exit }
      print n
      for (i = 1; i <= n; i++) print lines[i]
    }
  ' "$log" | {
    read -r count
    if [ "$count" = "none" ]; then
      echo "errors since the start: unknown (the log $log has no line \"cumin run starts\")"
    else
      echo "errors since the start: $count (log: $log)"
      sed -E 's/^.*"time":"([^"]*)".*"msg":"(([^"\\]|\\.)*)".*$/  \1 \2/'
    fi
  }
}

# Print the report, and set "unhealthy" to the reason when the last poll
# is old or has an error.
report() {
  unhealthy=""
  at="$(field last_poll.at)"
  at_epoch="$(epoch "$at")" || die "the monitor file $monitor is unreadable: last_poll.at is \"$at\", not a time in UTC"
  if [ -n "$fixed_now" ]; then
    now_epoch="$(epoch "$fixed_now")" || die "CUMIN_HEALTH_NOW is \"$fixed_now\", not a time in UTC"
  else
    now_epoch="$(date +%s)"
  fi
  age=$((now_epoch - at_epoch))
  echo "last poll: $at (${age}s ago)"

  errors="$(field last_poll.errors)" || errors=0
  echo "errors of the last poll: $errors"
  i=0
  while [ "$i" -lt "$errors" ]; do
    echo "  $(field "last_poll.errors.$i.repository"): $(field "last_poll.errors.$i.message")"
    i=$((i + 1))
  done

  print_log_errors

  agents="$(field agents)" || agents=0
  echo "agents at work: $agents"
  i=0
  while [ "$i" -lt "$agents" ]; do
    echo "  $(field "agents.$i.repository")#$(field "agents.$i.issue") $(field "agents.$i.role") ($(field "agents.$i.request")): $(field "agents.$i.title")"
    i=$((i + 1))
  done

  waiting="$(field waiting)" || waiting=0
  echo "waiting issues: $waiting"

  if [ "$age" -gt "$stale_after" ]; then
    unhealthy="the last poll is ${age}s old, over the limit of ${stale_after}s. cumin stopped, or the Host slept"
  elif [ "$errors" -gt 0 ]; then
    unhealthy="the last poll has $errors error(s)"
  fi
}

check_monitor

if [ "$wait_polls" -gt 0 ]; then
  started="$(date +%s)"
  seen=0
  last="$(field last_poll.at)"
  echo "waiting for $wait_polls poll(s) after $last, for at most ${timeout}s"
  while [ "$seen" -lt "$wait_polls" ]; do
    if [ $(($(date +%s) - started)) -ge "$timeout" ]; then
      report
      die "time limit: the last poll moved $seen time(s) of $wait_polls in ${timeout}s"
    fi
    sleep "$interval"
    check_monitor
    at="$(field last_poll.at)"
    [ "$at" != "$last" ] || continue
    last="$at"
    seen=$((seen + 1))
    errors="$(field last_poll.errors)" || errors=0
    if [ "$errors" -gt 0 ]; then
      echo "poll $seen of $wait_polls: $at, $errors error(s)"
      report
      die "the poll at $at has $errors error(s)"
    fi
    echo "poll $seen of $wait_polls: $at, no error"
  done
fi

report
[ -z "$unhealthy" ] || die "$unhealthy"
echo "cumin is running: the last poll is fresh and has no error"
