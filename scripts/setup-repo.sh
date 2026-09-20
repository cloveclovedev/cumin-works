#!/bin/sh
# Prepare a target repository for cumin with the administrator's own gh login:
# the protected-path workflow, a starter .cumin/config.toml, and two rulesets.
# cumin itself never uses administrator permissions, so a person runs this.
#
# Usage:
#   scripts/setup-repo.sh <owner>/<repo> --implementer-app <slug>
#       [--core-app <slug>] [--required-check <name>]... [--dry-run]
#
# Running the script again with the same arguments changes nothing.
# It needs only gh (logged in, with the "workflow" scope) and standard tools.
set -eu

here="$(cd "$(dirname "$0")" && pwd)/setup-repo"
workflow_path=".github/workflows/cumin-protected-paths.yml"
config_path=".cumin/config.toml"
protected_check="cumin-protected-paths"

usage() {
  sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

die() {
  echo "error: $*" >&2
  exit 1
}

repo=""
implementer_app=""
core_app=""
extra_checks=""
dry_run=0

while [ $# -gt 0 ]; do
  case "$1" in
    --implementer-app) [ $# -ge 2 ] || usage; implementer_app="$2"; shift 2 ;;
    --core-app) [ $# -ge 2 ] || usage; core_app="$2"; shift 2 ;;
    --required-check) [ $# -ge 2 ] || usage; extra_checks="${extra_checks}$2
"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) usage ;;
    -*) echo "unknown option: $1" >&2; usage ;;
    *) [ -z "$repo" ] || usage; repo="$1"; shift ;;
  esac
done

[ -n "$repo" ] && [ -n "$implementer_app" ] || usage
echo "$repo" | grep -Eq '^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$' || die "the repository must be <owner>/<repo>"
for slug in "$implementer_app" "$core_app"; do
  [ -z "$slug" ] || echo "$slug" | grep -Eq '^[a-z0-9][a-z0-9-]*$' || die "an App slug has only lower-case letters, digits, and '-': $slug"
done
# A check name goes into a JSON string as it is.
if printf '%s' "$extra_checks" | LC_ALL=C grep -q '["\\[:cntrl:]]'; then
  die "a check name must not contain a double quote, a backslash, or a control character"
fi

# --- Checks before any change -------------------------------------------------

gh auth status >/dev/null 2>&1 || die "gh is not logged in. Run: gh auth login"

# A classic token lists its scopes. A push of a workflow file needs "workflow".
scopes="$(gh api -i user 2>/dev/null | tr -d '\r' | sed -n 's/^[Xx]-[Oo][Aa]uth-[Ss]copes: //p')"
if [ -n "$scopes" ] && ! echo ", $scopes," | grep -q ', workflow,'; then
  die "the gh login has no \"workflow\" scope. Run: gh auth refresh -h github.com -s workflow"
fi

[ "$(gh api "repos/$repo" --jq '.permissions.admin')" = "true" ] || die "the gh login is not an administrator of $repo"
branch="$(gh api "repos/$repo" --jq '.default_branch')"

core_app_id=""
if [ -n "$core_app" ]; then
  core_app_id="$(gh api "apps/$core_app" --jq '.id')" || die "cannot read the App $core_app"
fi
# Only GitHub Actions may report the protected-path check.
actions_app_id="$(gh api apps/github-actions --jq '.id')"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# --- Render everything before any change ------------------------------------------

sed "s/__IMPLEMENTER_LOGIN__/${implementer_app}[bot]/" "$here/protected-paths.yml" >"$work/workflow.yml"
grep -q "__IMPLEMENTER_LOGIN__" "$work/workflow.yml" && die "the workflow template still holds the placeholder"
cp "$here/config.toml" "$work/config.toml"

# replace_text <file> <text to find> <replacement>
# Replaces the first match on each line by position. sed is not used here,
# because a check name can hold characters that sed reads as commands ("&", "|").
replace_text() {
  NEEDLE="$2" REPLACEMENT="$3" awk '
    BEGIN { needle = ENVIRON["NEEDLE"]; replacement = ENVIRON["REPLACEMENT"] }
    {
      i = index($0, needle)
      if (i > 0) { $0 = substr($0, 1, i - 1) replacement substr($0, i + length(needle)); found = 1 }
      print
    }
    END { if (!found) exit 3 }
  ' "$1"
}

# The JSON files are complete and valid as they are. The script only inserts the
# cumin-core App, the source of the protected-path check, and more checks.
if [ -n "$core_app_id" ]; then
  replace_text "$here/ruleset-protect-main.json" '"bypass_actors": [' \
    "\"bypass_actors\": [
    { \"actor_type\": \"Integration\", \"actor_id\": $core_app_id, \"bypass_mode\": \"always\" }," \
    >"$work/protect-main.json" || die "cannot find the bypass list in ruleset-protect-main.json"
else
  cp "$here/ruleset-protect-main.json" "$work/protect-main.json"
fi

checks="{ \"context\": \"$protected_check\", \"integration_id\": $actions_app_id }"
old_ifs="$IFS"
IFS='
'
for name in $extra_checks; do
  checks="$checks,
          { \"context\": \"$name\" }"
done
IFS="$old_ifs"
replace_text "$here/ruleset-main-required-checks.json" "{ \"context\": \"$protected_check\" }" "$checks" \
  >"$work/required-checks.json" || die "cannot find the protected-path check in ruleset-main-required-checks.json"

# --- The two files --------------------------------------------------------------

# put_file <path in the repository> <local file> <keep|report>
# "keep": an existing file is the Owner's. "report": say when it differs.
put_file() {
  if gh api -H "Accept: application/vnd.github.raw+json" "repos/$repo/contents/$1?ref=$branch" >"$work/current" 2>/dev/null; then
    if cmp -s "$work/current" "$2"; then
      echo "unchanged  $1"
    elif [ "$3" = "keep" ]; then
      echo "kept       $1 (it exists with other content; the script never overwrites it)"
    else
      echo "DIFFERENT  $1 (not overwritten)"
      echo "           Compare it with the rendered template, and change it with a pull request:"
      diff "$work/current" "$2" | sed 's/^/           /' || true
    fi
    return 0
  fi
  if [ "$dry_run" -eq 1 ]; then
    echo "would add  $1"
    return 0
  fi
  gh api -X PUT "repos/$repo/contents/$1" \
    -f message="chore: add $1 for cumin" \
    -f branch="$branch" \
    -f content="$(base64 <"$2" | tr -d '\n')" >/dev/null ||
    die "cannot add $1 to $branch. If a ruleset blocks the push, add the file with a pull request."
  echo "added      $1"
}

put_file "$workflow_path" "$work/workflow.yml" report
put_file "$config_path" "$work/config.toml" keep

# --- The two rulesets -----------------------------------------------------------

# apply_ruleset <local JSON file>
apply_ruleset() {
  name="$(sed -n 's/^  "name": "\(.*\)",$/\1/p' "$1")"
  id="$(gh api --paginate "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id" | head -n 1)"
  if [ "$dry_run" -eq 1 ]; then
    if [ -n "$id" ]; then echo "would update the ruleset $name"; else echo "would create the ruleset $name"; fi
    sed 's/^/           /' "$1"
    return 0
  fi
  if [ -n "$id" ]; then
    gh api -X PUT "repos/$repo/rulesets/$id" --input "$1" >/dev/null
    echo "applied    ruleset $name (it existed; now it matches the file)"
  else
    gh api -X POST "repos/$repo/rulesets" --input "$1" >/dev/null
    echo "created    ruleset $name"
  fi
}

apply_ruleset "$work/protect-main.json"
apply_ruleset "$work/required-checks.json"

if [ -z "$core_app" ]; then
  echo "note: no --core-app. Only repository administrators can update $branch. Run the script again with --core-app after the Apps exist."
fi
echo "done: $repo"
