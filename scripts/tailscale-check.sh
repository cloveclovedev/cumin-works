#!/bin/sh
# Check from both sides that the policy of the tailnet works: a member
# device reaches only the two ports of the Host, and the Host reaches no
# port of another device.
#
# Usage:
#   scripts/tailscale-check.sh member <host> [--other-port <port>]
#                                            [--timeout <seconds>]
#   scripts/tailscale-check.sh host [--timeout <seconds>]
#
# Run "member" on a member device, with the name or the address of the Host
# in the tailnet. The SSH port (22) and the screen sharing port (5900) of
# the Host must answer, and one other port (445 by default) must not.
#
# Run "host" on the Host. For each other device that "tailscale status
# --json" lists as online, port 22 and port 5900 must not answer.
#
# The script prints one line for each check: "ok:" or "fail:", the device,
# the port, and what the script expected. It connects to real TCP ports,
# because the ping of Tailscale answers even when the policy denies a port.
# It only reads: it changes nothing on the Host or in the policy.
#
# Each connection attempt ends after --timeout seconds (5 by default), so
# the script always ends.
#
# The exit code is 0 when every check is "ok", 2 for a wrong argument, and
# 1 otherwise: one check is "fail", or the script could not check anything.
set -eu

ssh_port=22
screen_sharing_port=5900
other_port=445
timeout=5

usage() {
  sed -n '2,27p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

die() {
  echo "error: $*" >&2
  exit 1
}

# A wrong value is a wrong call, like an unknown option: exit code 2.
bad_value() {
  echo "error: $*" >&2
  exit 2
}

is_number() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
  esac
}

[ $# -ge 1 ] || usage
mode="$1"
shift
target=""
case "$mode" in
  member)
    [ $# -ge 1 ] || usage
    target="$1"
    shift
    case "$target" in
      ''|-*) usage ;;
    esac
    ;;
  host) ;;
  -h|--help) usage ;;
  *) echo "unknown mode: $mode" >&2; usage ;;
esac

while [ $# -gt 0 ]; do
  case "$1" in
    --other-port) [ "$mode" = "member" ] && [ $# -ge 2 ] || usage; other_port="$2"; shift 2 ;;
    --timeout) [ $# -ge 2 ] || usage; timeout="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
done

is_number "$timeout" && [ "$timeout" -gt 0 ] || bad_value "--timeout needs a number of seconds above 0"
is_number "$other_port" && [ "$other_port" -gt 0 ] && [ "$other_port" -le 65535 ] || bad_value "--other-port needs a port number"
if [ "$other_port" -eq "$ssh_port" ] || [ "$other_port" -eq "$screen_sharing_port" ]; then
  bad_value "--other-port needs a port other than $ssh_port and $screen_sharing_port"
fi
command -v nc >/dev/null 2>&1 || die "nc is not on PATH"
# An nc that does not know -G fails at every call, and the script would
# read each failure as "no answer". So the script stops before the first
# check when the help of nc does not name -G.
case "$(nc -h 2>&1)" in
  *"-G "*) ;;
  *) die "the nc on PATH does not know the option -G (the limit of a connection attempt): use the nc of macOS, /usr/bin/nc" ;;
esac

failed=0

# Connect to one TCP port and print the line of the check. $1 is the name
# for the line, $2 the address, $3 the port, and $4 what the script
# expects: "answer" or "no answer". -G limits the connection attempt (man
# nc of macOS); -w alone does not.
check() {
  if nc -z -G "$timeout" -w "$timeout" "$2" "$3" >/dev/null 2>&1; then
    got="answer"
  else
    got="no answer"
  fi
  if [ "$got" = "$4" ]; then
    echo "ok: $1 port $3: expected $4, got $got"
  else
    echo "fail: $1 port $3: expected $4, got $got"
    failed=1
  fi
}

if [ "$mode" = "member" ]; then
  check "$target" "$target" "$ssh_port" "answer"
  check "$target" "$target" "$screen_sharing_port" "answer"
  check "$target" "$target" "$other_port" "no answer"
else
  command -v tailscale >/dev/null 2>&1 || die "tailscale is not on PATH: run this mode on the Host"
  command -v plutil >/dev/null 2>&1 || die "plutil is not on PATH: this mode works on macOS only"
  status="$(tailscale status --json)" || die "tailscale status --json failed: Tailscale is not running, or the Host has not joined the tailnet"

  # plutil reads JSON as well as a plist (man plutil), so no other tool is
  # needed. A dictionary gives its keys, one on each line.
  field() {
    printf '%s' "$status" | plutil -extract "$1" raw -o - - 2>/dev/null
  }

  # Peer holds the other devices, keyed by their public key (Tailscale
  # Docs "tailscale status", and the type ipnstate.Status that --json
  # prints). It is missing when the Host sees no other device.
  field Self >/dev/null || die "cannot read the output of tailscale status --json"
  peers="$(field Peer)" || peers=""
  checked=0
  for peer in $peers; do
    [ "$(field "Peer.$peer.Online")" = "true" ] || continue
    address="$(field "Peer.$peer.TailscaleIPs.0")" || continue
    name="$(field "Peer.$peer.DNSName" | tr -d '[:cntrl:]' | sed 's/\.$//')" || name=""
    [ -n "$name" ] || name="$address"
    check "$name" "$address" "$ssh_port" "no answer"
    check "$name" "$address" "$screen_sharing_port" "no answer"
    checked=$((checked + 1))
  done
  [ "$checked" -gt 0 ] || die "tailscale status --json lists no other device as online, so the script checked nothing"
fi

[ "$failed" -eq 0 ] || exit 1
