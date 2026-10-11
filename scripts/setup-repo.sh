#!/bin/sh
# Prepare a target repository for cumin with the administrator's own gh login:
# the protected-path workflow, a starter .cumin/config.toml, the rulesets, the
# priority labels that .cumin/config.toml names, and the labels of cumin.
# cumin itself never uses administrator permissions, so a person runs this.
#
# Usage:
#   scripts/setup-repo.sh <owner>/<repo> [--core-app <slug>]
#       [--implementer-app <slug>] [--required-check <name>]... [--dry-run]
#
# The script asks before it creates a priority label, and creates none without
# the answer "y". It creates the missing labels of cumin, and updates the color
# and the description of the ones that differ from the list of cumin.
# It asks before it deletes a label that cumin no longer uses, and deletes
# none without the answer "y", and none that an open issue or pull request carries.
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

workflow_differs=0
repo=""
core_app=""
implementer_app=""
extra_checks=""
dry_run=0

while [ $# -gt 0 ]; do
  case "$1" in
    --core-app) [ $# -ge 2 ] || usage; core_app="$2"; shift 2 ;;
    --implementer-app) [ $# -ge 2 ] || usage; implementer_app="$2"; shift 2 ;;
    --required-check)
      [ $# -ge 2 ] || usage
      # An empty name would be dropped without a word, and the check would not be required.
      [ -n "$2" ] || die "--required-check needs a name that is not empty"
      # The names are kept one for each line, and grep reads line by line, so a
      # line break inside a name must stop here.
      case "$2" in
        *"
"*) die "a check name must not contain a line break" ;;
      esac
      extra_checks="${extra_checks}$2
"
      shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) usage ;;
    -*) echo "unknown option: $1" >&2; usage ;;
    *) [ -z "$repo" ] || usage; repo="$1"; shift ;;
  esac
done

[ -n "$repo" ] || usage
echo "$repo" | grep -Eq '^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$' || die "the repository must be <owner>/<repo>"
for slug in "$core_app" "$implementer_app"; do
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
implementer_app_id=""
if [ -n "$implementer_app" ]; then
  implementer_app_id="$(gh api "apps/$implementer_app" --jq '.id')" || die "cannot read the App $implementer_app"
fi
# Only GitHub Actions may report the protected-path check.
actions_app_id="$(gh api apps/github-actions --jq '.id')"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# --- Render everything before any change ------------------------------------------

# The workflow names no App, so it goes into the repository as it is.
cp "$here/protected-paths.yml" "$work/workflow.yml"
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

# The Planner App may write only the branch cumin/diagrams, the one for the
# diagrams of its sub-issues: every other branch is for the administrators,
# cumin-core, and the Implementer App. Without both Apps, the ruleset would
# shut the Implementer out, so the script applies it only with both.
apply_branches=0
if [ -n "$core_app_id" ] && [ -n "$implementer_app_id" ]; then
  apply_branches=1
  replace_text "$here/ruleset-branches.json" '"bypass_actors": [' \
    "\"bypass_actors\": [
    { \"actor_type\": \"Integration\", \"actor_id\": $core_app_id, \"bypass_mode\": \"always\" },
    { \"actor_type\": \"Integration\", \"actor_id\": $implementer_app_id, \"bypass_mode\": \"always\" }," \
    >"$work/branches.json" || die "cannot find the bypass list in ruleset-branches.json"
fi
# Nobody deletes or force-pushes cumin/diagrams: an issue shows its diagrams
# by a commit of that branch.
cp "$here/ruleset-diagrams.json" "$work/diagrams.json"
# Only the administrators and cumin-core change tags.
if [ -n "$core_app_id" ]; then
  replace_text "$here/ruleset-tags.json" '"bypass_actors": [' \
    "\"bypass_actors\": [
    { \"actor_type\": \"Integration\", \"actor_id\": $core_app_id, \"bypass_mode\": \"always\" }," \
    >"$work/tags.json" || die "cannot find the bypass list in ruleset-tags.json"
else
  cp "$here/ruleset-tags.json" "$work/tags.json"
fi

checks="{ \"context\": \"$protected_check\", \"integration_id\": $actions_app_id }"
# Split on line breaks only, and do not expand "*" and "?" in a check name.
old_ifs="$IFS"
IFS='
'
set -f
for name in $extra_checks; do
  checks="$checks,
          { \"context\": \"$name\" }"
done
set +f
IFS="$old_ifs"
replace_text "$here/ruleset-main-required-checks.json" "{ \"context\": \"$protected_check\" }" "$checks" \
  >"$work/required-checks.json" || die "cannot find the protected-path check in ruleset-main-required-checks.json"

# --- The two files --------------------------------------------------------------

# put_file <path in the repository> <local file> <keep|report>
# "keep": an existing file is a Maintainer's. "report": say when it differs.
put_file() {
  # gh encodes the branch name as a query parameter. A branch name can hold "#" or "&".
  if gh api --method GET -H "Accept: application/vnd.github.raw+json" "repos/$repo/contents/$1" -f ref="$branch" >"$work/current" 2>"$work/read-error"; then
    if cmp -s "$work/current" "$2"; then
      echo "unchanged  $1"
    elif [ "$3" = "keep" ]; then
      echo "kept       $1 (it exists with other content; the script never overwrites it)"
    else
      workflow_differs=1
      echo "DIFFERENT  $1 (not overwritten)"
      echo "           Compare it with the template, and change it with a pull request:"
      diff "$work/current" "$2" | sed 's/^/           /' || true
    fi
    return 0
  fi
  # Only a confirmed 404 means that the file does not exist. Any other failure
  # (network, rate limit, 5xx) must not lead to a second copy or a half setup.
  grep -q "HTTP 404" "$work/read-error" || die "cannot read $1 from $branch: $(head -n 1 "$work/read-error")"
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

# --- The priority labels ------------------------------------------------------------

# The labels that priority_labels of .cumin/config.toml names belong to the
# organization, so cumin never creates them. The script lists the ones that
# the repository does not have, and creates them only when the person who
# runs it agrees. Without the key, cumin uses its default labels and creates
# them itself.

# priority_labels_of <file>
# Prints the names in the array of the key priority_labels, one for each line.
# It reads only quoted strings, on one line or on more lines. It does not
# decode the escapes of TOML: a name with a backslash in double quotes ends the
# function with status 4, so that the script never works on another name than
# the one that cumin reads.
priority_labels_of() {
  awk '
    # TOML allows the key bare or in quotes.
    !inside && /^[ \t]*("priority_labels"|\047priority_labels\047|priority_labels)[ \t]*=/ { inside = 1; sub(/^[^=]*=/, "") }
    inside {
      line = $0
      while (match(line, /"[^"]*"|\047[^\047]*\047|#|\]/)) {
        token = substr(line, RSTART, RLENGTH)
        # A comment ends the line, and "]" ends the array.
        if (token == "#") break
        if (token == "]") exit
        if (substr(token, 1, 1) == "\"" && index(token, "\\") > 0) exit 4
        print substr(token, 2, length(token) - 2)
        line = substr(line, RSTART + RLENGTH)
      }
    }
  ' "$1"
}

if gh api --method GET -H "Accept: application/vnd.github.raw+json" "repos/$repo/contents/$config_path" -f ref="$branch" >"$work/config-now" 2>"$work/read-error"; then
  priority_labels_of "$work/config-now" >"$work/priority-labels" ||
    die "cannot read priority_labels of $config_path: a label name uses a backslash. Write the names without escapes, or create the labels by hand"
else
  # Only a confirmed 404 means that the file does not exist (a dry run of a
  # new repository). Any other failure must not pass as "no priority labels".
  grep -q "HTTP 404" "$work/read-error" || die "cannot read $config_path from $branch: $(head -n 1 "$work/read-error")"
  : >"$work/priority-labels"
fi
if [ -s "$work/priority-labels" ]; then
  gh api --paginate "repos/$repo/labels" --jq '.[].name' >"$work/labels" || die "cannot list the labels of $repo"
  # GitHub label names ignore case.
  tr '[:upper:]' '[:lower:]' <"$work/labels" >"$work/labels-lower"
  : >"$work/missing-labels"
  while IFS= read -r label; do
    [ -n "$label" ] || continue
    printf '%s\n' "$label" | tr '[:upper:]' '[:lower:]' | grep -Fxq -f - "$work/labels-lower" ||
      printf '%s\n' "$label" >>"$work/missing-labels"
  done <"$work/priority-labels"
  if [ ! -s "$work/missing-labels" ]; then
    echo "unchanged  the priority labels of $config_path exist"
  else
    echo "missing    priority labels that $config_path names:"
    sed 's/^/           /' "$work/missing-labels"
    if [ "$dry_run" -eq 1 ]; then
      echo "would ask  whether to create them"
    else
      printf 'Create these labels in %s? [y/N] ' "$repo"
      answer=""
      # No answer (the end of the input) creates nothing.
      IFS= read -r answer || answer=""
      case "$answer" in
        y|Y|yes|YES|Yes)
          while IFS= read -r label; do
            gh api -X POST "repos/$repo/labels" -f name="$label" -f color="D4C5F9" \
              -f description="A Maintainer says: start this before a lower priority" >/dev/null ||
              die "cannot create the label $label"
            echo "created    label $label"
          done <"$work/missing-labels"
          ;;
        *) echo "kept       no label was created. cumin treats an issue without a priority label as the lowest priority" ;;
      esac
    fi
  fi
fi

# --- The labels of cumin --------------------------------------------------------

# repository_labels
# Prints the labels that cumin creates, one for each line, as
# "name|color|description": the same list as RepositoryLabels() of
# internal/workflow/labels.go. A test compares the two.
repository_labels() {
  cat <<'LABELS'
cumin/type/requirement|5319E7|This is a requirement issue
cumin/type/owner-task|5319E7|A Maintainer does this work by hand; cumin does not start it
cumin/status/ready|0E8A16|A Maintainer says: this issue can start
cumin/status/planning|1D76DB|The Planner splits the requirement
cumin/status/implementing|1D76DB|The Implementer works on the issue, or the sub-issues are in progress
cumin/status/reviewing|1D76DB|The Reviewer works on the pull request
cumin/status/checking|BFD4F2|GitHub runs the required checks
cumin/status/accepting|1D76DB|The Planner checks the merged work against the requirement
cumin/status/merging|1D76DB|cumin merges the pull request and closes the issue
cumin/status/awaiting-plan-review|FBCA04|Waiting for a Maintainer to review the plan and the sub-issues
cumin/status/awaiting-merge-decision|FBCA04|Waiting for a Maintainer to review the pull request and decide the merge
cumin/status/awaiting-acceptance|FBCA04|Waiting for a Maintainer to accept the requirement or send work back
cumin/status/awaiting-decision|D93F0B|cumin cannot go on; waiting for an answer of a Maintainer
risk/low|C2E0C6|A few lines with an obvious effect; cumin merges
risk/medium|FEF2C0|Everything else; a Maintainer merges
risk/high|F9D0C4|Cannot be undone by a revert; a Maintainer merges
LABELS
}

# default_priority_labels
# Prints the default priority labels of cumin in the form of
# repository_labels: the same list as DefaultPriorityLabels() of
# internal/workflow/labels.go. A test compares the two.
default_priority_labels() {
  cat <<'LABELS'
cumin/priority/P0|D4C5F9|A Maintainer says: start this before a lower priority
cumin/priority/P1|D4C5F9|A Maintainer says: start this before a lower priority
cumin/priority/P2|D4C5F9|A Maintainer says: start this before a lower priority
cumin/priority/P3|D4C5F9|A Maintainer says: start this before a lower priority
LABELS
}

# update_labels <list file> <create|keep>
# Brings the color and the description of each label of the list to the list.
# "create": a label that the repository does not have is created. "keep": it
# is left out. A label that priority_labels names is never changed.
update_labels() {
  tab="$(printf '\t')"
  while IFS='|' read -r label color description; do
    # GitHub label names ignore case.
    lower="$(printf '%s\n' "$label" | tr '[:upper:]' '[:lower:]')"
    if printf '%s\n' "$lower" | grep -Fxq -f - "$work/priority-labels-lower"; then
      echo "kept       label $label ($config_path names it as a priority label)"
      continue
    fi
    # The color in lower case and the description of the label today.
    current="$(LABEL="$lower" awk -F '\t' '
      tolower($1) == ENVIRON["LABEL"] { print tolower($2) "\t" substr($0, length($1) + length($2) + 3); exit }
    ' "$work/labels-now")"
    if [ -z "$current" ]; then
      [ "$2" = "create" ] || continue
      if [ "$dry_run" -eq 1 ]; then
        echo "would create the label $label"
        continue
      fi
      gh api -X POST "repos/$repo/labels" -f name="$label" -f color="$color" \
        -f description="$description" >/dev/null </dev/null || die "cannot create the label $label"
      echo "created    label $label"
      continue
    fi
    if [ "$current" = "$(printf '%s' "$color" | tr '[:upper:]' '[:lower:]')$tab$description" ]; then
      echo "unchanged  label $label"
      continue
    fi
    if [ "$dry_run" -eq 1 ]; then
      echo "would update the label $label"
      continue
    fi
    gh api -X PATCH "repos/$repo/labels/$label" -f color="$color" \
      -f description="$description" >/dev/null </dev/null || die "cannot update the label $label"
    echo "updated    label $label"
  done <"$1"
}

# retired_labels
# Prints the labels that cumin used in earlier versions and no longer uses,
# one for each line. A test proves that no name is in the lists of cumin.
retired_labels() {
  cat <<'LABELS'
cumin/status/awaiting-owner-review
cumin/status/awaiting-owner-decision
cumin/status/awaiting-checks
LABELS
}

# count_carriers <label> <open|closed>
# Prints how many issues and pull requests in that state carry the label.
# The list of the issues of a repository holds the pull requests too.
count_carriers() {
  gh api --paginate --method GET "repos/$repo/issues" -f labels="$1" -f state="$2" -f per_page=100 \
    --jq '.[].number' >"$work/carriers" </dev/null || die "cannot count the $2 issues and pull requests with the label $1"
  awk 'NF { n++ } END { print n + 0 }' "$work/carriers"
}

# delete_retired_labels
# For each label of retired_labels that the repository holds: prints how many
# issues and pull requests carry it, keeps it when an open one carries it, and
# asks before it deletes it otherwise. GitHub removes a deleted label from
# every issue and pull request, so only the answer "y" deletes.
delete_retired_labels() {
  for label in $(retired_labels); do
    # GitHub label names ignore case.
    LABEL="$label" awk -F '\t' '
      tolower($1) == ENVIRON["LABEL"] { found = 1 }
      END { exit !found }
    ' "$work/labels-now" || continue
    if printf '%s\n' "$label" | grep -Fxq -f - "$work/priority-labels-lower"; then
      echo "kept       label $label ($config_path names it as a priority label)"
      continue
    fi
    open="$(count_carriers "$label" open)"
    closed="$(count_carriers "$label" closed)"
    echo "retired    label $label: $open open and $closed closed issues and pull requests carry it"
    if [ "$open" -gt 0 ]; then
      echo "kept       label $label: $open open issues and pull requests carry it"
      continue
    fi
    if [ "$dry_run" -eq 1 ]; then
      echo "would ask  whether to delete the label $label"
      continue
    fi
    printf 'Delete the label %s of %s? The deletion removes the label from every closed issue and pull request. [y/N] ' "$label" "$repo"
    answer=""
    # No answer (the end of the input) deletes nothing.
    IFS= read -r answer || answer=""
    case "$answer" in
      y)
        gh api -X DELETE "repos/$repo/labels/$label" >/dev/null </dev/null || die "cannot delete the label $label"
        echo "deleted    label $label"
        ;;
      *) echo "kept       label $label: no answer \"y\"" ;;
    esac
  done
}

# apply_repository_labels
# Creates each label of repository_labels that the repository does not have,
# and updates the ones whose color or description differs from the list.
# cumin creates the same labels when it starts, so the script does not ask.
# The default priority labels are only updated, and only when
# .cumin/config.toml names no priority labels: cumin creates them itself.
# The retired labels come last.
apply_repository_labels() {
  # One line for each label: name, color, description. No description is an empty one.
  gh api --paginate "repos/$repo/labels" --jq '.[] | "\(.name)\t\(.color)\t\(.description // "")"' >"$work/labels-now" ||
    die "cannot list the labels of $repo"
  tr '[:upper:]' '[:lower:]' <"$work/priority-labels" >"$work/priority-labels-lower"
  repository_labels >"$work/cumin-labels"
  update_labels "$work/cumin-labels" create
  if [ ! -s "$work/priority-labels" ]; then
    default_priority_labels >"$work/default-priority-labels"
    update_labels "$work/default-priority-labels" keep
  fi
  delete_retired_labels
}

apply_repository_labels

# --- The rulesets ---------------------------------------------------------------

# apply_ruleset <local JSON file>
apply_ruleset() {
  name="$(sed -n 's/^  "name": "\(.*\)",$/\1/p' "$1")"
  # No pipe here: a pipe would hide a failure of gh, and the script would then
  # create a second ruleset with the same name.
  ids="$(gh api --paginate "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id")" ||
    die "cannot list the rulesets of $repo"
  id="${ids%%
*}"
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
if [ "$apply_branches" -eq 1 ]; then
  apply_ruleset "$work/branches.json"
fi
apply_ruleset "$work/diagrams.json"
apply_ruleset "$work/tags.json"

if [ -z "$core_app" ]; then
  echo "note: no --core-app. Only repository administrators can update $branch. Run the script again with --core-app after the Apps exist."
fi
if [ "$apply_branches" -eq 0 ]; then
  echo "note: the ruleset cumin-branches needs --core-app and --implementer-app. Until it exists, the Planner App can write every branch other than $branch."
fi
if [ "$workflow_differs" -eq 1 ]; then
  # The rulesets are applied all the same. A pull request that fixes the
  # workflow runs its own version of the workflow, so it can pass the check.
  die "$workflow_path differs from the template. The protected-path check may not work. Fix the file with a pull request, and run the script again."
fi
echo "done: $repo"
