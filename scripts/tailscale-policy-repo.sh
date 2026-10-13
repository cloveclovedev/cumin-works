#!/bin/sh
# Create the private repository that holds the policy of one tailnet, with
# the gh login of the Operator: policy.hujson from the template of
# cumin-works, and a workflow that tests the policy on a pull request and
# applies it on a push to the default branch.
#
# Usage:
#   scripts/tailscale-policy-repo.sh <owner>/<repo> --member <login>
#
# --member is the login of one member of the tailnet, as the admin console
# of Tailscale shows it. The tests of the policy use it: this member reaches
# the two ports of the Host, and the Host reaches no device of this member.
#
# The script creates the repository as private. When the repository exists,
# the script stops and changes nothing. At the end it prints the names of
# the secrets that a person must store by hand: the script reads and stores
# no credential.
# It needs only gh (logged in, with the "workflow" scope) and standard tools.
#
# The exit code is 0 when the repository holds the two files, 2 for a wrong
# argument, and 1 otherwise.
set -eu

here="$(cd "$(dirname "$0")" && pwd)/tailscale"
policy_path="policy.hujson"
workflow_path=".github/workflows/tailscale-policy.yml"
placeholder="__MEMBER_LOGIN__"

usage() {
  sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'
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

repo=""
member=""

while [ $# -gt 0 ]; do
  case "$1" in
    --member) [ $# -ge 2 ] || usage; member="$2"; shift 2 ;;
    -h|--help) usage ;;
    -*) echo "unknown option: $1" >&2; usage ;;
    *) [ -z "$repo" ] || usage; repo="$1"; shift ;;
  esac
done

[ -n "$repo" ] || usage
[ -n "$member" ] || usage
echo "$repo" | grep -Eq '^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$' || bad_value "the repository must be <owner>/<repo>"
# The login goes into a JSON string as it is.
if printf '%s' "$member" | LC_ALL=C grep -q '["\\[:cntrl:][:space:]]'; then
  bad_value "the login must not contain a double quote, a backslash, a space, or a control character"
fi
case "$member" in
  *"$placeholder"*) bad_value "the login must not be the placeholder of the template" ;;
esac

# --- Checks before any change -------------------------------------------------

gh auth status >/dev/null 2>&1 || die "gh is not logged in. Run: gh auth login"

# A classic token lists its scopes. A push of a workflow file needs "workflow".
scopes="$(gh api -i user 2>/dev/null | tr -d '\r' | sed -n 's/^[Xx]-[Oo][Aa]uth-[Ss]copes: //p')"
if [ -n "$scopes" ] && ! echo ", $scopes," | grep -q ', workflow,'; then
  die "the gh login has no \"workflow\" scope. Run: gh auth refresh -h github.com -s workflow"
fi

# Only a confirmed 404 means that the repository does not exist. Any other
# failure (network, rate limit, 5xx) must not lead to a call that writes.
if read_error="$(gh api "repos/$repo" 2>&1 >/dev/null)"; then
  die "the repository $repo exists. The script changes nothing in a repository that exists. Change its policy with a pull request."
fi
case "$read_error" in
  *"HTTP 404"*) ;;
  *) die "cannot read whether $repo exists: $read_error" ;;
esac

# --- Render everything before any change --------------------------------------

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# awk replaces by position. sed is not used here, because a login can hold
# characters that sed reads as commands ("&", "|").
NEEDLE="$placeholder" REPLACEMENT="$member" awk '
  BEGIN { needle = ENVIRON["NEEDLE"]; replacement = ENVIRON["REPLACEMENT"] }
  {
    rest = $0
    line = ""
    while ((i = index(rest, needle)) > 0) {
      line = line substr(rest, 1, i - 1) replacement
      rest = substr(rest, i + length(needle))
      found = 1
    }
    print line rest
  }
  END { if (!found) exit 3 }
' "$here/policy.hujson" >"$work/policy.hujson" || die "cannot find the placeholder $placeholder in the template of the policy"
cp "$here/policy-workflow.yml" "$work/workflow.yml"

# --- The repository and its two files -----------------------------------------

gh repo create "$repo" --private >/dev/null || die "cannot create the private repository $repo"
echo "created    $repo (private)"

# add_file <path in the repository> <local file>
# Without a branch name, GitHub writes to the default branch, and the first
# file of an empty repository creates that branch.
add_file() {
  gh api -X PUT "repos/$repo/contents/$1" \
    -f message="chore: add $1" \
    -f content="$(base64 <"$2" | tr -d '\n')" >/dev/null ||
    die "cannot add $1 to $repo. The repository exists now, so the script does not run again: add the file by hand."
  echo "added      $1"
}

# The policy goes first, so that the first run of the workflow finds it.
add_file "$policy_path" "$work/policy.hujson"
add_file "$workflow_path" "$work/workflow.yml"

# --- What a person does by hand -----------------------------------------------

cat <<EOF

Store these secrets in $repo by hand (Settings > Secrets and variables > Actions):
  TS_TAILNET        the name of the tailnet
  TS_OAUTH_ID       the ID of an OAuth client with the scope "policy_file"
  TS_OAUTH_SECRET   the secret of that OAuth client
Create the OAuth client in the admin console of Tailscale.
The first run of the workflow fails, because the secrets are missing. Run it again after you store them.
EOF
