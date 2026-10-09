#!/bin/sh
# Wait until the monitor file of cumin shows an issue that waits for a person
# and that this script did not report before.
#
#   wait-for-waiting.sh --seen <path> [--timeout <seconds>] [--interval <seconds>]
#                       [--stale <seconds>] [--file <path>]
#
# The script reads the monitor file at the interval. When the list "waiting"
# holds an entry that the file of --seen does not hold, the script prints
# each new entry on one line and ends:
#
#   <kind> TAB <repository> TAB <issue> TAB <title> TAB <URL>
#
# An entry is the same when its repository, its issue, and its kind are the
# same, as in the menu bar app. The entries go to the standard output, and
# every other message goes to the standard error.
#
# Exit code:
#   0    at least one new entry is printed
#   2    a wrong argument, or the file of --seen cannot be written
#   124  the time limit ended with no new entry: the message holds "time
#        limit", the reason of a failed last read, and an old last_poll.at
#
# The file of --seen holds the entries that wait and that the script
# reported, one line each. After a good read, the script writes the entries
# of that read there, so an entry that left the list and comes back is new
# again. A read that fails is skipped: a missing file, an empty file, a file
# that is not JSON, and a file that is not a monitor file of version 1. Such
# a read never counts as "nothing waits", and it keeps the file of --seen.
#
# The script reads one local file. It does not read GitHub, and it does not
# change the monitor file. It needs only standard tools of macOS: sh, perl
# with its core modules JSON::PP and Time::Local, awk, cmp, date, mktemp,
# mv, sleep.
#
# Defaults: --timeout 1500, --interval 10, --stale 180 (the limit of the menu
# bar app for an old last_poll.at), --file ~/.local/state/cumin/monitor.json.

set -u

TAB=$(printf '\t')

fail() {
	echo "error: $*" >&2
	exit 2
}

usage="usage: wait-for-waiting.sh --seen <path> [--timeout <seconds>] [--interval <seconds>] [--stale <seconds>] [--file <path>]"
seen=
timeout=1500
interval=10
stale=180
file=${HOME:-}/.local/state/cumin/monitor.json
while [ $# -gt 0 ]; do
	case $1 in
	--seen | --timeout | --interval | --stale | --file)
		[ $# -ge 2 ] || fail "$1 needs a value. $usage"
		case $1 in
		--seen) seen=$2 ;;
		--timeout) timeout=$2 ;;
		--interval) interval=$2 ;;
		--stale) stale=$2 ;;
		--file) file=$2 ;;
		esac
		shift 2
		;;
	*) fail "unknown argument '$1'. $usage" ;;
	esac
done
[ -n "$seen" ] || fail "--seen <path> is missing. $usage"
for pair in "--timeout=$timeout" "--interval=$interval" "--stale=$stale"; do
	printf '%s\n' "${pair#*=}" | grep -Eq '^[1-9][0-9]*$' || fail "${pair%%=*} must be a number of seconds, got '${pair#*=}'"
done
if [ -e "$seen" ] && { [ ! -f "$seen" ] || [ ! -r "$seen" ]; }; then
	fail "the file of --seen '$seen' is not a file that the script can read"
fi

work=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$work"' EXIT
trap 'rm -rf "$work"; trap - EXIT; exit 129' HUP
trap 'rm -rf "$work"; trap - EXIT; exit 130' INT
trap 'rm -rf "$work"; trap - EXIT; exit 143' TERM

# read_monitor writes one good read of the monitor file to $work/read: the
# line "ok TAB <last_poll.at> TAB <its seconds since 1970>", then one line
# for each entry of "waiting". It fails, with the reason in $work/reason, when
# the read is not good. The two last fields of the first line are empty when
# the file has no last_poll.at or when it is not a time.
read_monitor() {
	perl -MJSON::PP -MTime::Local -e '
		sub skip { print STDERR "$_[0]\n"; exit 1 }
		open(my $in, "<", $ARGV[0]) or skip("the file is missing or cannot be read");
		local $/;
		my $text = <$in>;
		defined $text && $text =~ /\S/ or skip("the file is empty");
		my $data = eval { JSON::PP->new->utf8->decode($text) };
		ref $data eq "HASH" or skip("the file is not valid JSON");
		my $version = $data->{version};
		defined $version && !ref $version && $version =~ /^[0-9]+$/ && $version == 1
			or skip("the file is not a monitor file of version 1");
		my $waiting = $data->{waiting};
		ref $waiting eq "ARRAY" or skip("the file has no list \"waiting\"");
		my $at = ref $data->{last_poll} eq "HASH" ? $data->{last_poll}{at} : undef;
		my ($shown, $seconds) = ("", "");
		if (defined $at && !ref $at
			&& $at =~ /^(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d):(\d\d)(?:\.\d+)?Z$/) {
			my $time = eval { timegm($6, $5, $4, $3, $2 - 1, $1) };
			($shown, $seconds) = ($at, $time) if defined $time;
		}
		my $lines = ["ok\t$shown\t$seconds"];
		for my $entry (@$waiting) {
			ref $entry eq "HASH" or skip("an entry of \"waiting\" is not an object");
			my $fields = [];
			for my $key (qw(kind repository issue title url)) {
				my $value = $entry->{$key};
				if (!defined $value || ref $value || $value eq "") {
					$key eq "title" || $key eq "url" or skip("an entry of \"waiting\" has no $key");
					$value = "";
				}
				$value =~ s/[\t\r\n]+/ /g;
				push @$fields, $value;
			}
			push @$lines, join("\t", @$fields);
		}
		binmode STDOUT, ":utf8";
		print "$_\n" for @$lines;
	' "$file" >"$work/read" 2>"$work/reason"
}

start=$(date +%s)
deadline=$((start + timeout))
good_reads=0
last_failed=
at=
at_seconds=
while :; do
	if read_monitor; then
		good_reads=$((good_reads + 1))
		last_failed=
		IFS=$TAB read -r _ at at_seconds <"$work/read"
		awk 'NR > 1' "$work/read" >"$work/entries"
		awk -F "$TAB" '{ print $1 FS $2 FS $3 }' "$work/entries" >"$work/keys"
		awk -F "$TAB" -v seen="$seen" '
			BEGIN { while ((getline line < seen) > 0) reported[line] = 1 }
			!(($1 FS $2 FS $3) in reported)
		' "$work/entries" >"$work/new"
		[ ! -s "$work/new" ] || cat "$work/new"
		if ! cmp -s "$work/keys" "$seen" 2>/dev/null; then
			# The new file replaces the old one in one step, so a stop of
			# the script leaves one of the two.
			cp "$work/keys" "$seen.tmp.$$" 2>/dev/null && mv "$seen.tmp.$$" "$seen" 2>/dev/null || {
				rm -f "$seen.tmp.$$"
				fail "the file of --seen '$seen' cannot be written, so the next call reports the same entries again"
			}
		fi
		[ ! -s "$work/new" ] || exit 0
	else
		last_failed=$(head -n 1 "$work/reason")
		[ -n "$last_failed" ] || last_failed="the file cannot be read"
	fi
	now=$(date +%s)
	[ "$now" -lt "$deadline" ] || break
	left=$((deadline - now))
	[ "$left" -lt "$interval" ] || left=$interval
	sleep "$left"
done

message="the time limit of $timeout seconds ended with no new entry."
if [ "$good_reads" -eq 0 ]; then
	message="$message No read of the monitor file '$file' was good ($last_failed), so nothing is known about the issues that wait."
else
	[ -z "$last_failed" ] || message="$message The last read of the monitor file '$file' failed ($last_failed)."
	if [ -z "$at_seconds" ]; then
		message="$message The monitor file has no last_poll.at that is a time, so cumin may not run."
	elif [ $((now - at_seconds)) -gt "$stale" ]; then
		message="$message last_poll.at is $at, $((now - at_seconds)) seconds old (the limit is $stale), so cumin may not run."
	fi
fi
echo "$message" >&2
exit 124
