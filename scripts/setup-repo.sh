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
# the answer "y". It creates the missing labels of cumin, and asks once before
# it replaces the old status labels of the open issues and pull requests.
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
# "keep": an existing file is the Owner's. "report": say when it differs.
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
              -f description="The Owner says: start this before a lower priority" >/dev/null ||
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
cumin/type/owner-task|5319E7|The Owner does this work by hand; cumin does not start it
cumin/status/ready|0E8A16|The Owner says: this issue can start
cumin/status/planning|1D76DB|The Planner splits the requirement
cumin/status/implementing|1D76DB|The Implementer works on the issue, or the sub-issues are in progress
cumin/status/awaiting-checks|BFD4F2|The Implementer is done; waiting for the required checks
cumin/status/reviewing|1D76DB|The Reviewer works on the pull request
cumin/status/awaiting-owner-review|FBCA04|Waiting for the Owner to review and approve
cumin/status/awaiting-owner-decision|D93F0B|The agent cannot continue; waiting for a decision of the Owner
cumin/status/checking|BFD4F2|GitHub runs the required checks
cumin/status/accepting|1D76DB|The Planner checks the merged work against the requirement
cumin/status/merging|1D76DB|cumin merges the pull request and closes the issue
cumin/status/awaiting-plan-review|FBCA04|Waiting for the Owner to review the plan and the sub-issues
cumin/status/awaiting-merge-decision|FBCA04|Waiting for the Owner to review the pull request and decide the merge
cumin/status/awaiting-acceptance|FBCA04|Waiting for the Owner to accept the requirement or send work back
cumin/status/awaiting-decision|D93F0B|cumin cannot go on; waiting for an answer of the Owner
risk/low|C2E0C6|A few lines with an obvious effect; cumin merges
risk/medium|FEF2C0|Everything else; the Owner merges
risk/high|F9D0C4|Cannot be undone by a revert; the Owner merges
LABELS
}

# new_status_label <old label> <requirement issue: yes|no> <sub-issues> <closed sub-issues>
# Prints the new status label that replaces an old one: the table of the move
# of the labels in issue-states.md. A requirement issue whose sub-issues are
# all closed waits for the acceptance; any other one waits for the plan review.
# A label that is not an old status label ends the function with status 1.
new_status_label() {
  case "$1" in
    cumin/status/awaiting-checks) echo "cumin/status/checking" ;;
    cumin/status/awaiting-owner-decision) echo "cumin/status/awaiting-decision" ;;
    cumin/status/awaiting-owner-review)
      if [ "$2" != "yes" ]; then
        echo "cumin/status/awaiting-merge-decision"
      elif [ "$3" -gt 0 ] && [ "$3" -eq "$4" ]; then
        echo "cumin/status/awaiting-acceptance"
      else
        echo "cumin/status/awaiting-plan-review"
      fi ;;
    *) return 1 ;;
  esac
}

# create_repository_labels
# Creates each label of repository_labels that the repository does not have.
# cumin creates the same labels when it starts, so the script does not ask.
create_repository_labels() {
  gh api --paginate "repos/$repo/labels" --jq '.[].name' >"$work/labels" || die "cannot list the labels of $repo"
  # GitHub label names ignore case.
  tr '[:upper:]' '[:lower:]' <"$work/labels" >"$work/labels-lower"
  repository_labels >"$work/cumin-labels"
  missing=0
  while IFS='|' read -r label color description; do
    if printf '%s\n' "$label" | tr '[:upper:]' '[:lower:]' | grep -Fxq -f - "$work/labels-lower"; then
      continue
    fi
    missing=1
    if [ "$dry_run" -eq 1 ]; then
      echo "would create the label $label"
      continue
    fi
    gh api -X POST "repos/$repo/labels" -f name="$label" -f color="$color" \
      -f description="$description" >/dev/null </dev/null || die "cannot create the label $label"
    echo "created    label $label"
  done <"$work/cumin-labels"
  if [ "$missing" -eq 0 ]; then
    echo "unchanged  the labels of cumin exist"
  fi
}

# move_status_labels
# Lists the open issues and pull requests that carry an old status label, asks
# once, and then replaces the label: it adds the new label first and removes
# the old one after that, so that a failure in between leaves both and a
# second run finishes the move. Run it only after the installed cumin decides
# with the new names.
move_status_labels() {
  : >"$work/label-moves"
  for old in cumin/status/awaiting-checks cumin/status/awaiting-owner-decision cumin/status/awaiting-owner-review; do
    # The list holds issues and pull requests. sub_issues_summary counts the
    # sub-issues of an issue and the closed ones.
    gh api --paginate --method GET "repos/$repo/issues" -f state=open -f labels="$old" -f per_page=100 \
      --jq '.[] | [.number, (if .pull_request then "pull-request" else "issue" end), (if ([.labels[].name] | index("cumin/type/requirement")) then "yes" else "no" end), (.sub_issues_summary.total // 0), (.sub_issues_summary.completed // 0)] | @tsv' \
      >"$work/label-holders" </dev/null || die "cannot list the open issues with the label $old"
    while IFS='	' read -r number kind requirement total closed; do
      [ -n "$number" ] || continue
      new="$(new_status_label "$old" "$requirement" "$total" "$closed")" || die "no new label for $old"
      printf '%s\t%s\t%s\t%s\n' "$number" "$kind" "$old" "$new" >>"$work/label-moves"
    done <"$work/label-holders"
  done
  if [ ! -s "$work/label-moves" ]; then
    echo "unchanged  no open issue or pull request carries an old status label"
    return 0
  fi
  echo "old        status labels on open issues and pull requests:"
  while IFS='	' read -r number kind old new; do
    echo "           #$number ($kind): $old -> $new"
  done <"$work/label-moves"
  if [ "$dry_run" -eq 1 ]; then
    echo "would ask  whether to replace them"
    return 0
  fi
  printf 'Replace these labels in %s? Answer y only when the installed cumin decides with the new names. [y/N] ' "$repo"
  answer=""
  # No answer (the end of the input) replaces nothing.
  IFS= read -r answer || answer=""
  case "$answer" in
    y|Y|yes|YES|Yes) ;;
    *)
      echo "kept       no label of an issue was replaced"
      return 0 ;;
  esac
  while IFS='	' read -r number kind old new; do
    gh api -X POST "repos/$repo/issues/$number/labels" -f "labels[]=$new" >/dev/null </dev/null ||
      die "cannot add the label $new to #$number"
    # A "/" of the label name is a part of the path, so it is encoded.
    gh api -X DELETE "repos/$repo/issues/$number/labels/$(printf '%s' "$old" | sed 's|/|%2F|g')" >/dev/null </dev/null ||
      die "cannot remove the label $old from #$number"
    echo "replaced   #$number: $old -> $new"
  done <"$work/label-moves"
}

create_repository_labels
move_status_labels

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
