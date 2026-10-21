// The values of the dashboard page, from site/trace.json, the output of
// `cumin-trace trace` (docs/ja/designs/dashboard/page.md). The functions are
// pure, apart from the reads of the trace and of the titles of the areas, and
// run at build time only (CTR-DASH-007).
import { readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { documentTitle } from "./requirements.mjs";

const isString = (value) => typeof value === "string";

// readTrace returns the trace of <root>/site/trace.json, or undefined when
// the file is missing, is no JSON, or has not the form of the trace
// (CTR-DASH-006). It reads the file with readFileSync, so that a missing
// file does not stop the build.
export function readTrace(root) {
  let trace;
  try {
    trace = JSON.parse(readFileSync(join(root, "site", "trace.json"), "utf8"));
  } catch {
    return undefined;
  }
  if (trace === null || typeof trace !== "object") return undefined;
  if (!isString(trace.repository) || !isString(trace.read_at)) return undefined;
  if (!Array.isArray(trace.requirements) || !Array.isArray(trace.discrepancies)) return undefined;
  for (const r of trace.requirements) {
    if (r === null || typeof r !== "object") return undefined;
    if (!isString(r.id) || !isString(r.status) || !isString(r.file) || !Array.isArray(r.requirement_issues)) return undefined;
  }
  return trace;
}

const japan = new Intl.DateTimeFormat("en-US", { timeZone: "Asia/Tokyo", hourCycle: "h23", year: "numeric", month: "numeric", day: "numeric", hour: "numeric", minute: "numeric", second: "numeric" });

// japanTime returns read_at, a UTC time of the trace, in the time zone
// Asia/Tokyo as "<year>-<month>-<day> <hour>:<minute>:<second>": no leading
// zero in the month, the day, and the hour, and two digits for the minute and
// the second (CTR-DASH-006). The time zone of the build machine does not
// change it. It throws a RangeError for a value that is no time.
export function japanTime(readAt) {
  const parts = Object.fromEntries(japan.formatToParts(new Date(readAt)).map((part) => [part.type, part.value]));
  // With hourCycle "h23", "numeric" still gives the hour in two digits, so
  // each part is read as a number and written again.
  const one = (value) => String(Number(value));
  const two = (value) => one(value).padStart(2, "0");
  return `${parts.year}-${one(parts.month)}-${one(parts.day)} ${one(parts.hour)}:${two(parts.minute)}:${two(parts.second)}`;
}

const progressLabels = { not_filed: "未起票", in_progress: "進行中", done: "完了" };

// planned returns the requirement issues that are not closed as not planned.
function planned(requirement) {
  return requirement.requirement_issues.filter((issue) => issue.state_reason !== "not_planned");
}

// milestones returns the different milestones of the requirement issues that
// are not closed as not planned, in the order of their titles, and "なし"
// at the end when one of the issues has no milestone.
function milestones(requirement) {
  const issues = planned(requirement);
  const titles = [...new Set(issues.map((issue) => issue.milestone).filter((m) => m !== null && m !== undefined))].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  if (issues.some((issue) => issue.milestone === null || issue.milestone === undefined)) titles.push("なし");
  return titles;
}

// subIssues returns each sub-issue of the requirement issues once, in the
// order of the first appearance.
function subIssues(requirement) {
  const seen = new Set();
  const found = [];
  for (const issue of requirement.requirement_issues) {
    for (const sub of issue.sub_issues) {
      const key = `${sub.repository}#${sub.number}`;
      if (seen.has(key)) continue;
      seen.add(key);
      found.push({ repository: sub.repository, number: sub.number, closed: sub.state === "closed" });
    }
  }
  return found;
}

// tree returns the rows of the requirement tree (CTR-DASH-001): one row for
// each category where it first shows, then its requirements in the order of
// the trace, each child one level below its parent. A deprecated requirement
// and its descendants have no row. A category row holds the file of its first
// requirement, for areaTitle(), and the counts of its requirements with no
// children by progress, and their number (leafCount). A requirement row holds
// its ID without the prefix and the category (short), its progress (state, or
// "parent" for a requirement with children and no progress, from an older
// trace), the text of the progress (the count of the children for a
// requirement with children), and its implementation issues closed and in
// all.
export function tree(trace) {
  const byID = new Map(trace.requirements.map((r) => [r.id, r]));
  const hidden = (r) => {
    for (let at = r; at !== undefined; at = at.parent ? byID.get(at.parent) : undefined) {
      if (at.status === "deprecated") return true;
    }
    return false;
  };
  const depth = (r) => {
    let n = 0;
    for (let at = r; at.parent && byID.has(at.parent); at = byID.get(at.parent)) n++;
    return n;
  };
  const shown = trace.requirements.filter((r) => !hidden(r));
  const categoryOf = (r) => r.id.split("-")[1] ?? "";
  const rows = [];
  const categories = new Map();
  for (const r of shown) {
    const name = categoryOf(r);
    if (!categories.has(name)) {
      const row = { kind: "category", id: name, depth: 0, progress: "", file: r.file, counts: { done: 0, in_progress: 0, not_filed: 0 }, leafCount: 0 };
      categories.set(name, row);
      rows.push(row);
    }
    const category = categories.get(name);
    const children = r.children !== null && r.children !== undefined;
    if (!children) {
      category.leafCount++;
      if (Object.hasOwn(category.counts, r.progress)) category.counts[r.progress]++;
    }
    const subs = subIssues(r);
    rows.push({
      kind: "requirement",
      id: r.id,
      short: r.id.split("-").slice(2).join("-"),
      title: r.title ?? "",
      depth: depth(r) + 1,
      leaf: !children,
      state: children && !Object.hasOwn(progressLabels, r.progress) ? "parent" : r.progress,
      progress: children ? `完了 ${r.children.done} / ${r.children.total}` : (progressLabels[r.progress] ?? ""),
      issues: planned(r).map((issue) => issue.number),
      milestones: milestones(r),
      implementation: r.implementation_issues.total === 0 ? "" : `${r.implementation_issues.closed} / ${r.implementation_issues.total}`,
      closed: r.implementation_issues.closed,
      total: r.implementation_issues.total,
      subIssues: subs,
    });
  }
  for (const category of categories.values()) category.progress = `完了 ${category.counts.done} / ${category.leafCount}`;
  return rows;
}

// areaTitle returns the name of a category on the dashboard: the title of the
// document that holds its first requirement, as the site shows it, or the
// category ID when that document is not in docs_dir (an old trace).
export function areaTitle(settings, root, row) {
  const file = row.file ?? "";
  const inDocs = relative(join(root, settings.docsDir), join(root, file));
  if (file === "" || inDocs === "" || inDocs.startsWith("..")) return row.id;
  try {
    return documentTitle(readFileSync(join(root, file), "utf8"), file) || row.id;
  } catch {
    return row.id;
  }
}

// requirementURL returns the URL of the page and the anchor of a requirement
// ID, from the table of idTable(), or undefined for an ID that no document
// has (CTR-DASH-005).
export function requirementURL(table, id) {
  const page = table.get(id);
  return page === undefined ? undefined : `${page}#${id}`;
}

// issueURL returns the URL of an issue on GitHub (CTR-DASH-005).
export function issueURL(repository, number) {
  return `https://github.com/${repository}/issues/${number}`;
}

const discrepancyLabels = { unknown_id: "要件文書にないID", open_issue_for_deprecated: "廃止した要件の開いた要求Issue" };

// discrepancies returns the discrepancies of the trace in their order
// (CTR-DASH-009): the kind, its text on the page (the kind itself for a kind
// that the page does not know), the number of the requirement issue, and the
// ID.
export function discrepancies(trace) {
  return trace.discrepancies.map((d) => ({ kind: d.kind, label: discrepancyLabels[d.kind] ?? String(d.kind), issue: d.issue, id: d.id }));
}
