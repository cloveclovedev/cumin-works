#!/bin/sh
# Wait until one pull request is merged, then call the general tool that
# replaces the binary of the Host.
#
#   wait-for-merge.sh <owner>/<repo> <number> [--timeout <seconds>] [--interval <seconds>]
#
# The script reads the pull request with gh at the interval (REST "Get a pull
# request": the fields "state" and "merged"). The monitor file of cumin does
# not hold the merge of a pull request, so this script reads GitHub. When the
# pull request is merged, the script calls
# $CUMIN_SOURCE_DIR/scripts/replace-binary.sh one time, with no option.
#
# Exit code:
#   0    the pull request is merged, and the tool passed
#   1    the pull request is closed without a merge, and nothing is replaced;
#        or the pull request is merged, and the tool failed
#   2    a wrong argument, CUMIN_SOURCE_DIR is not set, or the tool is missing
#   124  the time limit ended before a read showed the merge: the message
#        holds "time limit" and the last read, and nothing is replaced
#
# Only the answer "closed" with "merged: true" is a merge. A read that fails,
# an empty read, and any other answer are skipped: such a read never counts
# as "merged". One read ends after 30 seconds, so a gh that hangs does not
# hold the script. The time limit is checked after each read, so the script
# ends at most 30 seconds after the limit, and the message names a last read
# that had its full time.
#
# The script only reads GitHub: it runs gh api with the method GET. The tool
# changes the Host (docs/ja/guides/host-tools.md of the checkout). The time
# limit covers the wait for the merge, not the tool, which has its own limits.
#
# The script needs gh and standard tools (sh, date, grep, head, mktemp, sleep).
# Defaults: --timeout 1800, --interval 30.

set -u

fail() {
	echo "error: $*" >&2
	exit 2
}

usage="usage: wait-for-merge.sh <owner>/<repo> <number> [--timeout <seconds>] [--interval <seconds>]"
repo=
number=
timeout=1800
interval=30
# The longest time of one read (seconds). The tests set this.
read_limit=${CUMIN_MERGE_READ_LIMIT:-30}
positional=0
while [ $# -gt 0 ]; do
	case $1 in
	--timeout | --interval)
		[ $# -ge 2 ] || fail "$1 needs a value. $usage"
		case $1 in
		--timeout) timeout=$2 ;;
		--interval) interval=$2 ;;
		esac
		shift 2
		;;
	-*) fail "unknown argument '$1'. $usage" ;;
	*)
		positional=$((positional + 1))
		case $positional in
		1) repo=$1 ;;
		2) number=$1 ;;
		*) fail "unknown argument '$1'. $usage" ;;
		esac
		shift
		;;
	esac
done
[ "$positional" -eq 2 ] || fail "$usage"
printf '%s\n' "$repo" | grep -Eq '^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$' || fail "the repository must be <owner>/<repo>, got '$repo'"
printf '%s\n' "$number" | grep -Eq '^[1-9][0-9]*$' || fail "the number of the pull request must be a number, got '$number'"
for pair in "--timeout=$timeout" "--interval=$interval" "CUMIN_MERGE_READ_LIMIT=$read_limit"; do
	printf '%s\n' "${pair#*=}" | grep -Eq '^[1-9][0-9]*$' || fail "${pair%%=*} must be a number of seconds, got '${pair#*=}'"
done

# The tool is checked before the wait, so that a wrong path shows at once
# and not after the merge.
[ -n "${CUMIN_SOURCE_DIR:-}" ] || fail "CUMIN_SOURCE_DIR is not set. Set it to the checkout that holds scripts/replace-binary.sh"
tool=$CUMIN_SOURCE_DIR/scripts/replace-binary.sh
[ -f "$tool" ] && [ -x "$tool" ] || fail "'$tool' is not a file that the script can run. Set CUMIN_SOURCE_DIR to the checkout that holds scripts/replace-binary.sh"
command -v gh >/dev/null 2>&1 || fail "gh is not on PATH"

work=$(mktemp -d) || fail "mktemp failed"
child=
watchdog=
cleanup() {
	[ -n "$child" ] && kill "$child" 2>/dev/null
	[ -n "$watchdog" ] && kill "$watchdog" 2>/dev/null
	rm -rf "$work"
}
trap cleanup EXIT
trap 'cleanup; trap - EXIT; exit 129' HUP
trap 'cleanup; trap - EXIT; exit 130' INT
trap 'cleanup; trap - EXIT; exit 143' TERM

# read_pull writes one read of the pull request to $work/read: the line
# "<state> <merged>". It fails, with the reason in $work/reason, when gh
# fails or when gh does not end within $read_limit seconds.
read_pull() {
	: >"$work/read"
	: >"$work/reason"
	gh api "repos/$repo/pulls/$number" --jq '[.state, (.merged | tostring)] | join(" ")' >"$work/read" 2>"$work/reason" &
	child=$!
	(
		trap 'kill "$sleeper" 2>/dev/null; exit 0' TERM
		sleep "$read_limit" &
		sleeper=$!
		wait "$sleeper" && : >"$work/stopped" && kill "$child" 2>/dev/null
	) >/dev/null 2>&1 &
	watchdog=$!
	wait "$child"
	status=$?
	kill "$watchdog" 2>/dev/null
	wait "$watchdog" 2>/dev/null
	child=
	watchdog=
	if [ -f "$work/stopped" ]; then
		rm -f "$work/stopped"
		echo "gh did not end in $read_limit seconds" >"$work/reason"
		return 1
	fi
	return "$status"
}

start=$(date +%s)
deadline=$((start + timeout))
last=
while :; do
	if read_pull; then
		answer=$(head -n 1 "$work/read")
		case $answer in
		"closed true") break ;;
		"closed false")
			echo "error: the pull request $number of $repo is closed without a merge. Nothing is replaced." >&2
			exit 1
			;;
		"open false") last="the pull request is open" ;;
		"") last="gh returned nothing" ;;
		*) last="gh returned '$answer', which is not a state of a pull request" ;;
		esac
	else
		reason=$(head -n 1 "$work/reason")
		last="gh could not read the pull request: ${reason:-no message}"
	fi
	now=$(date +%s)
	if [ "$now" -ge "$deadline" ]; then
		echo "error: time limit: no read showed the merge of the pull request $number of $repo in $timeout seconds (the last read: $last). Nothing is replaced." >&2
		exit 124
	fi
	left=$((deadline - now))
	pause=$interval
	[ "$left" -ge "$pause" ] || pause=$left
	sleep "$pause"
done

echo "the pull request $number of $repo is merged. Calling $tool"
"$tool"
status=$?
if [ "$status" -ne 0 ]; then
	echo "error: replace-binary.sh ended with the exit code $status. Its last lines say which step failed and what state cumin is in." >&2
	exit 1
fi
exit 0
