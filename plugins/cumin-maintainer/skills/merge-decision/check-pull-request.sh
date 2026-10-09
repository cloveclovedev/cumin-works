#!/bin/sh
# Check one pull request that waits for the merge decision of a Maintainer.
#
#   check-pull-request.sh <owner>/<repo> <number>
#
# The script reads GitHub once and does not wait. It prints the head commit,
# the latest deciding review (APPROVED or CHANGES_REQUESTED) of each author
# with its commit, each required check of the base branch with its state on
# the head commit, and each changed file under a protected path.
#
# Exit code:
#   0    the pull request is open and not a draft, an approval is on the head
#        commit, no author requests changes on the head commit, and every
#        required check passed
#   1    the pull request is not ready: the last line says why
#   2    a wrong argument, a failed read, or an empty read; nothing is decided
#   124  the time limit ended; nothing is decided
#
# Only the latest deciding review of an author counts, as in cumin. GitHub
# keeps the state APPROVED of an older review, so an approval that the same
# author replaced with a change request is not an approval.
#
# A required check passes only with the conclusion "success". A cancelled, a
# queued, a skipped, a neutral, and a missing check are not a pass. cumin and
# GitHub count "skipped" and "neutral" as passed; this script does not. A branch whose rules
# require no check is not a pass either, because an empty list once counted
# as "passed".
#
# The required checks come from the rules of the base branch (REST "Get rules
# for a branch"), as cumin reads them. A rule that names an App is met only
# by a check run of that App. A commit status meets only a rule that names no
# App.
#
# The protected paths come from .cumin/config.toml of the default branch. The
# rules of matching are those of the cumin-protected-paths check, with one
# difference: this script folds the case of ASCII letters only, and it does
# not fold the Unicode form.
#
# The script needs gh and standard tools (sh, awk, grep, sed, sort, mktemp, sleep).
# The time limit is 60 seconds. CUMIN_CHECK_TIME_LIMIT changes it (seconds).

set -u

DEFAULT_ENTRIES='.cumin/
CLAUDE.md
AGENTS.md
.claude/'
CONFIG_PATH=.cumin/config.toml
TAB=$(printf '\t')

fail() {
	echo "error: $*" >&2
	exit 2
}

[ $# -eq 2 ] || fail "usage: check-pull-request.sh <owner>/<repo> <number>"
repo=$1
number=$2
printf '%s\n' "$repo" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || fail "the repository must be <owner>/<repo>, got '$repo'"
printf '%s\n' "$number" | grep -Eq '^[1-9][0-9]*$' || fail "the number of the pull request must be a number, got '$number'"
limit=${CUMIN_CHECK_TIME_LIMIT:-60}
printf '%s\n' "$limit" | grep -Eq '^[1-9][0-9]*$' || fail "CUMIN_CHECK_TIME_LIMIT must be a number of seconds, got '$limit'"

work=$(mktemp -d) || fail "mktemp failed"
child=
watchdog=
cleanup() {
	[ -n "$watchdog" ] && kill "$watchdog" 2>/dev/null
	rm -rf "$work"
}
trap cleanup EXIT
on_limit() {
	[ -n "$child" ] && kill "$child" 2>/dev/null
	echo "error: the time limit of $limit seconds ended before the reads did; nothing is decided" >&2
	exit 124
}
trap on_limit TERM
# A shell does not always run the EXIT trap after a signal, so a signal stops
# the watchdog here.
trap 'cleanup; trap - EXIT; exit 129' HUP
trap 'cleanup; trap - EXIT; exit 130' INT

# The watchdog ends the script at the time limit. Its output is closed, so
# that a caller that reads the output of the script does not wait for it.
(
	trap 'kill "$sleeper" 2>/dev/null; exit 0' TERM
	sleep "$limit" &
	sleeper=$!
	wait "$sleeper" && kill -TERM $$
) >/dev/null 2>&1 &
watchdog=$!

# read_gh <name> <gh arguments> writes the output of one gh call to
# $work/<name> and returns the exit code of gh. gh runs in the background,
# because a shell runs a trap only after a foreground command ends.
read_gh() {
	out=$1
	shift
	gh "$@" >"$work/$out" 2>"$work/$out.err" &
	child=$!
	wait "$child"
	status=$?
	child=
	return "$status"
}

# read_list <name> <what> <gh arguments> reads a list of several pages. The
# jq filter of the caller prints the line "page" for each page, before the
# rows of the page. An answer without that line is an empty read, not an
# empty list. A row always holds a tab, so no row is the line "page".
read_list() {
	name=$1
	what=$2
	shift 2
	read_gh "$name.raw" "$@" || fail "gh could not read $what: $(cat "$work/$name.raw.err")"
	grep -qx page "$work/$name.raw" || fail "gh returned nothing for $what"
	grep -vx page "$work/$name.raw" >"$work/$name"
	return 0
}

# 1. The pull request: head commit, base branch, number of changed files.
read_gh pull api "repos/$repo/pulls/$number" \
	--jq '[.head.sha, .base.ref, (.changed_files | tostring), (if .draft then "draft" else .state end)] | join("\t")' ||
	fail "gh could not read the pull request: $(cat "$work/pull.err")"
IFS=$TAB read -r head base changed state <"$work/pull" || true
printf '%s\n' "${head:-}" | grep -Eq '^[0-9a-f]{40,64}$' || fail "gh returned no head commit for the pull request"
[ -n "${base:-}" ] || fail "gh returned no base branch for the pull request"
printf '%s\n' "${changed:-}" | grep -Eq '^[0-9]+$' || fail "gh returned no number of changed files for the pull request"

# 2. The deciding reviews, oldest first, as GitHub lists them.
read_list reviews "the reviews" api --paginate "repos/$repo/pulls/$number/reviews?per_page=100" \
	--jq '"page", (.[] | select(.state == "APPROVED" or .state == "CHANGES_REQUESTED") | [(.user.login // "unknown"), .commit_id, .state] | join("\t"))'

# 3. The checks that the rules of the base branch require.
branch=$(printf '%s' "$base" | sed -e 's/%/%25/g' -e 's|/|%2F|g' -e 's/#/%23/g' -e 's/?/%3F/g' -e 's/ /%20/g')
read_list required "the rules of the branch $base" api --paginate "repos/$repo/rules/branches/$branch?per_page=100" \
	--jq '"page", (.[] | select(.type == "required_status_checks") | .parameters.required_status_checks[] | [.context, (.integration_id // 0 | tostring)] | join("\t"))'

# 4. The check runs and the commit statuses of the head commit.
read_list runs "the check runs of the head commit" api --paginate "repos/$repo/commits/$head/check-runs?per_page=100" \
	--jq '"page", (.check_runs[] | [.name, (.app.id // 0 | tostring), .status, (.conclusion // "")] | join("\t"))'
read_list statuses "the commit statuses of the head commit" api --paginate "repos/$repo/commits/$head/status?per_page=100" \
	--jq '"page", (.statuses[] | [.context, .state] | join("\t"))'

# 5. The protected paths. Without a ref, GitHub reads the default branch. A
# missing file gives the default list, as the cumin-protected-paths check does.
if read_gh config api -H "Accept: application/vnd.github.raw+json" "repos/$repo/contents/$CONFIG_PATH"; then
	awk '
	function bad(why) { print "error: protected_paths in .cumin/config.toml: " why > "/dev/stderr"; failed = 1; exit 2 }
	{ text = text $0 "\n" }
	END {
		if (failed) exit 2
		n = split(text, lines, "\n")
		# The key is a top-level key: the search ends at the first table header.
		for (i = 1; i <= n; i++) {
			if (lines[i] ~ /^[ \t]*\[/) break
			if (lines[i] ~ /^[ \t]*protected_paths[ \t]*=/) { start = i; break }
		}
		if (!start) exit 4
		rest = lines[start]
		sub(/^[^=]*=/, "", rest)
		for (i = start + 1; i <= n; i++) rest = rest "\n" lines[i]
		state = "before"
		for (p = 1; p <= length(rest); p++) {
			c = substr(rest, p, 1)
			if (state == "comment") { if (c == "\n") state = back; continue }
			if (state == "string") {
				if (c == quote) { entries[++count] = entry; state = "after"; continue }
				if (c == "\n" || c == "\\") bad("a string with a line break or a \"\\\" is not a valid entry")
				entry = entry c
				continue
			}
			if (c == "\n" && state == "before") bad("the value must be an array of strings")
			if (c == " " || c == "\t" || c == "\n" || c == "\r") continue
			if (c == "#" && state != "before") { back = state; state = "comment"; continue }
			if (c == "]" && state != "before") { for (i = 1; i <= count; i++) print entries[i]; exit 0 }
			if (state == "before" && c == "[") { state = "value"; continue }
			if (state == "value" && (c == "\"" || c == "\047")) { quote = c; entry = ""; state = "string"; continue }
			if (state == "after" && c == ",") { state = "value"; continue }
			bad("the value must be an array of strings")
		}
		bad("the array does not end")
	}' "$work/config" >"$work/entries"
	case $? in
	0) ;;
	4) printf '%s\n' "$DEFAULT_ENTRIES" >"$work/entries" ;;
	*) exit 2 ;;
	esac
elif grep -q 'HTTP 404' "$work/config.err"; then
	printf '%s\n' "$DEFAULT_ENTRIES" >"$work/entries"
else
	fail "gh could not read $CONFIG_PATH: $(cat "$work/config.err")"
fi

# 6. The changed files. A rename gives two paths: the new one and the old one.
read_list files "the changed files" api --paginate "repos/$repo/pulls/$number/files?per_page=100" \
	--jq '"page", (.[] | [.status, .filename, (.previous_filename // "")] | join("\t"))'
listed=$(grep -c . "$work/files")
[ "$listed" -eq "$changed" ] || fail "gh listed $listed of $changed changed files; the check cannot see every file"

# 7. The head commit once more. A push during the reads would mix the facts
# of two commits.
read_gh pull.again api "repos/$repo/pulls/$number" --jq '.head.sha' ||
	fail "gh could not read the pull request again: $(cat "$work/pull.again.err")"
[ "$(cat "$work/pull.again")" = "$head" ] || fail "the head commit changed during the reads; run the script again"

# Every read is done. The rest decides and prints, so the watchdog stops.
kill "$watchdog" 2>/dev/null
watchdog=
trap '' TERM

# The latest deciding review of each author, in the order of the first one.
awk -F '\t' '
!($1 in latest) { order[++authors] = $1 }
{ latest[$1] = $0 }
END { for (i = 1; i <= authors; i++) print latest[order[i]] }' "$work/reviews" >"$work/reviews.latest" || fail "awk could not sort the reviews"

awk -F '\t' '
function bad(entry, why) { print "error: protected_paths: \"" entry "\" " why > "/dev/stderr"; failed = 1; exit 2 }
# matches applies the rules of matching of the cumin-protected-paths check.
function matches(e, path,    n, m, i, last) {
	n = split(tolower(path), part, "/")
	m = count[e]
	if (anchored[e]) {
		if (n < m) return 0
		for (i = 1; i <= m; i++) if (part[i] != parts[e, i]) return 0
		return n > m || !directory[e]
	}
	# A name without an inner "/" matches at any depth. A directory entry
	# never matches the last part, because that part is a file.
	last = directory[e] ? n - 1 : n
	for (i = 1; i <= last; i++) if (part[i] == parts[e, 1]) return 1
	return 0
}
FILENAME == ARGV[1] {
	entry = $0
	total++
	names[total] = entry
	if (index(entry, "!") == 1 || index(entry, "*") || index(entry, "?") || index(entry, "[") || index(entry, "\\")) bad(entry, "uses a wildcard, \"!\" or \"\\\"")
	core = entry
	directory[total] = sub(/\/$/, "", core)
	anchored[total] = index(core, "/") > 0
	sub(/^\//, "", core)
	count[total] = split(tolower(core), piece, "/")
	if (count[total] == 0) bad(entry, "is not a valid path")
	for (i = 1; i <= count[total]; i++) {
		if (piece[i] == "" || piece[i] == "." || piece[i] == "..") bad(entry, "is not a valid path")
		parts[total, i] = piece[i]
	}
	next
}
{
	for (f = 2; f <= 3; f++) {
		if ($f == "") continue
		for (e = 1; e <= total; e++) if (matches(e, $f)) {
			print "- " $f " (" $1 ") matches \"" names[e] "\""
			break
		}
	}
}
END { if (failed) exit 2 }' "$work/entries" "$work/files" >"$work/protected" || exit 2

# One line for each required check: the name, "pass" or "fail", the states.
sort -u "$work/required" >"$work/required.sorted"
awk -F '\t' '
FILENAME == ARGV[1] { run[++runs] = $0; next }
FILENAME == ARGV[2] { status[++statuses] = $0; next }
{
	states = ""
	pass = 1
	found = 0
	for (i = 1; i <= runs; i++) {
		split(run[i], r, "\t")
		if (r[1] != $1 || ($2 != "0" && r[2] != $2)) continue
		s = r[3] == "completed" ? r[4] : r[3]
		found++
		if (s != "success") pass = 0
		states = states (states == "" ? "" : ", ") (s == "" ? "unknown" : s)
	}
	if ($2 == "0") for (i = 1; i <= statuses; i++) {
		split(status[i], r, "\t")
		if (r[1] != $1) continue
		found++
		if (r[2] != "success") pass = 0
		states = states (states == "" ? "" : ", ") (r[2] == "" ? "unknown" : r[2])
	}
	if (!found) { pass = 0; states = "missing" }
	print $1 "\t" (pass ? "pass" : "fail") "\t" states
}' "$work/runs" "$work/statuses" "$work/required.sorted" >"$work/checks" || fail "awk could not compare the checks"

reasons=
add_reason() {
	reasons="${reasons:+$reasons; }$1"
}

echo "Pull request $number of $repo (${state:-unknown})"
[ "${state:-}" = open ] || add_reason "the pull request is ${state:-unknown}, not open"
echo "Head commit: $head"
echo
echo "Reviews (the latest APPROVED or CHANGES_REQUESTED review of each author):"
approvals=0
approved=0
while IFS=$TAB read -r login commit review; do
	where="not the head commit"
	[ "$commit" = "$head" ] && where="the head commit"
	if [ "$review" = APPROVED ]; then
		approvals=1
		[ "$commit" = "$head" ] && approved=1
		echo "- $login approves ${commit:-unknown} ($where)"
	else
		echo "- $login requests changes on ${commit:-unknown} ($where)"
		[ "$commit" = "$head" ] && add_reason "$login requests changes on the head commit"
	fi
done <"$work/reviews.latest"
[ -s "$work/reviews.latest" ] || echo "- none"
if [ "$approvals" -eq 0 ]; then
	add_reason "no approval"
elif [ "$approved" -eq 0 ]; then
	add_reason "no approval is on the head commit"
fi

echo
echo "Required checks of the branch $base, on the head commit:"
while IFS=$TAB read -r name verdict states; do
	echo "- $name: $states"
	[ "$verdict" = pass ] || add_reason "the check $name is $states"
	case $states in
	*cancelled*) echo "  A cancelled check is not a failed check. Propose to run it again." ;;
	esac
done <"$work/checks"
if [ ! -s "$work/checks" ]; then
	echo "- none: the rules of the branch require no check"
	add_reason "the list of required checks is empty"
fi

echo
echo "Changed files under a protected path ($changed changed files):"
if [ -s "$work/protected" ]; then
	cat "$work/protected"
else
	echo "- none"
fi

echo
if [ -z "$reasons" ]; then
	echo "Result: an approval is on the head commit, no author requests changes on it, and every required check passed."
	exit 0
fi
echo "Result: not ready. $reasons."
exit 1
