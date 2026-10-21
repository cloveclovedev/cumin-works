// The hast plugin of Sätteri that gives each requirement heading an anchor
// from its ID, and turns each reference to a known ID into a link to its page
// and anchor, also across pages (CTR-SITE-003). It puts each requirement in
// one block, with the ID apart from the title (CTR-SITE-006). The places and
// the rules of a requirement and of a reference are those of the format check
// (docs/ja/designs/check/reading.md). It also rewrites the relative links
// between documents (CTR-SITE-010). The script diagramScript opens a
// diagram that is clicked (CTR-SITE-007). sidebar() gives the items of the
// sidebar in the reading order (CTR-SITE-011).
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { basename, dirname, extname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { defineHastPlugin } from "satteri";

// diagramScript opens the file of an image of the body when a click lands
// on the image. The browser shows the SVG alone, at its own size, and the
// back button returns to the page (CTR-SITE-007).
export const diagramScript =
  'document.addEventListener("click",(e)=>{const i=e.target.closest?.(".sl-markdown-content img");if(i)location.href=i.currentSrc||i.src;});';

export const escape = (text) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// idPattern returns the form of the IDs of the product with the prefix
// (CTR-FMT-001), with the "g" flag when global is true.
function idPattern(prefix, global) {
  const id = `${escape(prefix)}-[A-Z0-9]+-[0-9]{3}(?:\\.[1-9][0-9]*)?`;
  return global ? new RegExp(id, "g") : new RegExp(`^${id}$`);
}

// references returns the start and the end of each reference in the text
// (CTR-FMT-010). The start and the end of the text count as characters that
// are no letter, no digit, and no "-".
export function references(prefix, text) {
  const refs = [];
  for (const match of text.matchAll(idPattern(prefix, true))) {
    const from = match.index;
    const to = from + match[0].length;
    if (from > 0 && /[A-Za-z0-9-]/.test(text[from - 1])) continue;
    if (to < text.length && /[A-Za-z0-9]/.test(text[to])) continue;
    if (text[to] === "." && /[0-9]/.test(text[to + 1] ?? "")) continue;
    refs.push([from, to]);
  }
  return refs;
}

// pageID returns the ID of the page of a document, from its path relative to
// docs_dir. The loader gives it to glob() as generateId, so that pageURL and
// the pages of Starlight follow one rule: each part of the path with no ".md",
// in lower case, with "-" for each run of characters that are no letter, no
// digit, no "_", and no "-". An index.md gives the ID of its directory.
export function pageID(path) {
  const parts = path.split(sep).join("/").replace(/\.md$/, "").split("/");
  return parts.map((part) => part.toLowerCase().replace(/[^\p{L}\p{N}_-]+/gu, "-")).join("/").replace(/\/index$/, "");
}

// pageURL returns the URL of the page of a document, from its path relative
// to docs_dir.
export function pageURL(base, path) {
  const id = pageID(path);
  return base + (id === "index" ? "" : id + "/");
}

// The frontmatter as Astro 7.4.0 finds it: a copy of the expressions of
// parseFrontmatter() in @astrojs/internal-helpers. check-site compares the
// result with the pages of Astro, through the positions of the headings.
const frontmatterRE = /(?:^\uFEFF?|^\s*\n)(?:---|\+\+\+)([\s\S]*?\n)(?:---|\+\+\+)/;
const frontmatterTypeRE = /(?:^\uFEFF?|^\s*\n)(---|\+\+\+)/;

// frontmatter returns the text of the frontmatter of a document, or
// undefined when it has none.
export function frontmatter(text) {
  return frontmatterRE.exec(text)?.[1];
}

// body returns the body of a document as the loader of Astro gives it to the
// Markdown processor: the frontmatter blanked with spaces, then the whole text
// trimmed. Line numbers of the body are those of the positions of the nodes.
export function body(text) {
  const raw = frontmatter(text);
  if (raw === undefined) return text.trim();
  const fence = frontmatterTypeRE.exec(text)[1];
  return text.replace(`${fence}${raw}${fence}`, `   ${raw.replace(/[^\r\n]/g, " ")}   `).trim();
}

// atxHeadings returns the line number and the text of each ATX heading of a
// body, outside the code blocks.
export function atxHeadings(text) {
  const lines = text.split(/\r?\n/);
  const headings = [];
  let fence = "";
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const open = line.match(/^ {0,3}(`{3,}|~{3,})/);
    if (fence) {
      if (open && open[1][0] === fence[0] && open[1].length >= fence.length && line.trim() === open[1]) fence = "";
      continue;
    }
    if (open) {
      fence = open[1];
      continue;
    }
    const heading = line.match(/^ {0,3}#{1,6}(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$/);
    if (heading) headings.push({ line: i + 1, source: line, text: heading[1] ?? "" });
  }
  return headings;
}

// firstHeading returns the title from the first ATX heading of a body, with
// no code span quotes, and the line of the heading. A body with no heading
// gets the file name with no extension, and the line 0.
export function firstHeading(text, filePath) {
  const [first] = atxHeadings(text);
  if (first === undefined) return { title: basename(filePath, extname(filePath)), line: 0 };
  return { title: first.text.replace(/`/g, "").trim(), line: first.line };
}

// dotPath reports whether a part of the path starts with ".". The docs
// collection loads no such document (glob("**/*.md")), so it has no page.
export function dotPath(path) {
  return path.split(sep).some((part) => part.startsWith("."));
}

// markdownFiles returns the Markdown files below dir, at any depth, with no
// file under a directory or with a name that starts with ".".
function markdownFiles(dir) {
  let entries;
  try {
    entries = readdirSync(dir, { withFileTypes: true, recursive: true });
  } catch {
    return [];
  }
  return entries
    .filter((e) => e.isFile() && e.name.endsWith(".md"))
    .map((e) => join(e.parentPath, e.name))
    .filter((file) => !dotPath(relative(dir, file)));
}

// headingIDs returns the IDs of the requirement headings of a document: an
// ATX heading outside the frontmatter and the code blocks whose first part is
// a code span with an ID of the right form (CTR-FMT-003).
export function headingIDs(prefix, text) {
  const valid = idPattern(prefix, false);
  const ids = [];
  for (const { source } of atxHeadings(body(text))) {
    const heading = source.match(/^ {0,3}#{1,6}[ \t]+`([^`]*)`/);
    const id = heading?.[1].replace(/^ (.*) $/, "$1");
    if (id !== undefined && valid.test(id)) ids.push(id);
  }
  return ids;
}

// idTable returns the URL of the page of each requirement ID, from the
// documents under requirements_dir.
export function idTable(settings, root) {
  const docs = join(root, settings.docsDir);
  const table = new Map();
  if (!settings.prefix) return table;
  for (const file of markdownFiles(join(root, settings.requirementsDir))) {
    const url = pageURL(settings.base, relative(docs, file));
    for (const id of headingIDs(settings.prefix, readFileSync(file, "utf8"))) {
      if (!table.has(id)) table.set(id, url);
    }
  }
  return table;
}

const headingTags = ["h1", "h2", "h3", "h4", "h5", "h6"];

// requirementID returns the content of the first code span of a requirement
// heading, or undefined for any other element.
function requirementID(node, ctx, prefix) {
  if (node.type !== "element" || !headingTags.includes(node.tagName)) return undefined;
  if (ctx.parent(node)?.type !== "root") return undefined;
  const first = node.children.find((child) => child.type !== "text" || child.value.trim() !== "");
  if (first?.type !== "element" || first.tagName !== "code") return undefined;
  const content = ctx.textContent(first);
  return content.startsWith(`${prefix}-`) ? content : undefined;
}

// isStatus reports whether the element is the list item of a 状態 field: an
// item of an unordered list at the top level of the document (CTR-FMT-005).
function isStatus(node, ctx) {
  if (node.type !== "element" || node.tagName !== "li") return false;
  const list = ctx.parent(node);
  if (list?.tagName !== "ul" || ctx.parent(list)?.type !== "root") return false;
  return /^\s*状態:/.test(ctx.textContent(node));
}

// hasTitle reports whether the frontmatter of the document has a key title,
// plain or quoted, at the start of a line. check-site uses it; the plugin
// reads the data of the frontmatter that Astro parsed.
export function hasTitle(text) {
  return /^["']?title["']?[ \t]*:/m.test(frontmatter(text) ?? "");
}

// documentTitle returns the title of a document: the title of its
// frontmatter, or its first heading.
export function documentTitle(text, file) {
  if (!hasTitle(text)) return firstHeading(body(text), file).title;
  const line = frontmatter(text).match(/^["']?title["']?[ \t]*:[ \t]*(.*)$/m)[1];
  return line.replace(/^(["'])(.*)\1[ \t]*$/, "$2").trim();
}

// bodyLines returns the lines of a document outside the frontmatter, the code
// blocks, and the HTML comments. The page shows none of them.
export function bodyLines(text) {
  const lines = body(text).split(/\r?\n/);
  const found = [];
  let fence = "";
  let comment = false;
  for (let i = 0; i < lines.length; i++) {
    let line = lines[i];
    if (comment) {
      const end = line.indexOf("-->");
      if (end === -1) continue;
      line = " ".repeat(end + 3) + line.slice(end + 3);
      comment = false;
    } else {
      const open = line.match(/^ {0,3}(`{3,}|~{3,})/);
      if (fence) {
        if (open && open[1][0] === fence[0] && open[1].length >= fence.length && line.trim() === open[1]) fence = "";
        continue;
      }
      if (open) {
        fence = open[1];
        continue;
      }
    }
    // A comment starts outside the code spans, and ends at the first "-->".
    // Only a comment at the start of a line (an HTML block) goes on to the
    // next lines; an unclosed "<!--" in the middle of a line is text.
    for (;;) {
      const start = line.replace(/`[^`]*`/g, (span) => " ".repeat(span.length)).indexOf("<!--");
      if (start === -1) break;
      const end = line.indexOf("-->", start + 4);
      if (end === -1) {
        if (/^ {0,3}$/.test(line.slice(0, start))) {
          line = "";
          comment = true;
        }
        break;
      }
      line = line.slice(0, start) + " ".repeat(end + 3 - start) + line.slice(end + 3);
    }
    found.push(line);
  }
  return found;
}

// sourceLinks returns the destination and the text of each inline link of a
// document that is no image, outside the frontmatter, the code blocks, and
// the code spans.
export function sourceLinks(text) {
  const found = [];
  for (const line of bodyLines(text)) {
    const plain = line.replace(/`[^`]*`/g, (span) => span.replace(/[[\]()]/g, " "));
    for (const match of plain.matchAll(/(?<!!)\[([^\]]*)\]\(<?([^)\s>]+)>?(?:\s+"[^"]*")?\)/g)) {
      found.push({ text: line.slice(match.index + 1, match.index + 1 + match[1].length), href: match[2] });
    }
  }
  return found;
}

// headingLevel returns the level of a heading element, or 0 for any other
// node.
function headingLevel(node) {
  return node.type === "element" && headingTags.includes(node.tagName) ? Number(node.tagName[1]) : 0;
}

// blocks returns the children of the root with each requirement in a
// <section class="requirement">, from its heading to the next heading of the
// same or a higher level. A child requirement nests in its parent. A
// horizontal rule that ends a block is removed (CTR-SITE-006).
export function blocks(children, isRequirement) {
  const top = [];
  const stack = [];
  for (const child of children) {
    const level = headingLevel(child);
    if (level > 0) {
      while (stack.length > 0 && stack[stack.length - 1].level >= level) stack.pop();
    }
    const into = stack.length > 0 ? stack[stack.length - 1].section.children : top;
    if (level > 0 && isRequirement(child)) {
      const section = {
        type: "element",
        tagName: "section",
        properties: { className: ["requirement"] },
        children: [child],
      };
      into.push(section);
      stack.push({ level, section });
    } else {
      into.push(child);
    }
  }
  trimRules(top);
  return top;
}

// trimRules removes the horizontal rule at the end of each section, at any
// depth.
function trimRules(children) {
  for (const child of children) {
    if (child.type !== "element" || child.tagName !== "section") continue;
    trimRules(child.children);
    let last = child.children.length - 1;
    while (last >= 0 && child.children[last].type === "text" && child.children[last].value.trim() === "") last--;
    if (last >= 0 && child.children[last].type === "element" && child.children[last].tagName === "hr") {
      child.children.splice(last);
    }
  }
}

// inside reports whether the file is below the directory dir.
function inside(dir, file) {
  const rel = relative(dir, file);
  return rel !== "" && !rel.startsWith("..") && !isAbsolute(rel);
}

// linkTarget returns the kind of the destination of a link in the document
// file, from the rules of docs/ja/designs/site/build.md: "keep" for an
// external URL, a path from the top, or a heading of the same page; "page"
// with its URL for a document of the site; "puml" with its file for a
// readable diagram source; and "text" for any other relative path, also for
// a document of a dot path, which has no page. A "page" also has the file of
// the document.
export function linkTarget(settings, root, file, href) {
  if (href === "" || href.startsWith("#") || href.startsWith("/") || /^[A-Za-z][A-Za-z0-9+.-]*:/.test(href)) {
    return { kind: "keep" };
  }
  const at = href.search(/[?#]/);
  const path = at < 0 ? href : href.slice(0, at);
  const hash = at < 0 || href[at] !== "#" ? "" : href.slice(at);
  let decoded;
  try {
    decoded = decodeURIComponent(path);
  } catch {
    return { kind: "text" };
  }
  const docs = join(root, settings.docsDir);
  const target = resolve(dirname(file), decoded);
  const readable = inside(docs, target) && existsSync(target) && statSync(target).isFile();
  if (readable && target.endsWith(".md") && !dotPath(relative(docs, target))) return { kind: "page", url: pageURL(settings.base, relative(docs, target)) + hash, file: target };
  if (readable && target.endsWith(".puml")) return { kind: "puml", file: target };
  return { kind: "text" };
}

// sidebar returns the items of the sidebar of Starlight in the reading order
// (CTR-SITE-011): the page of the entry document, then a depth-first walk of
// the links of each index.md. The index.md of a directory makes a group of
// the pages below that directory. Each page shows once, where the walk first
// places it. A page that the walk does not place goes, in path order, to the
// end of the deepest group that holds it, or to the end of the top level.
// The items name no directory above docs_dir (CTR-SITE-012).
export function sidebar(settings, root) {
  const docs = join(root, settings.docsDir);
  const path = (file) => relative(docs, file).split(sep).join("/");
  const pages = markdownFiles(docs).map(path).sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  const placed = new Set();
  const groups = new Map();
  const top = [];
  // group returns a new group of the directory dir, with the page of its
  // index.md first when one exists.
  const group = (dir, label) => {
    const index = `${dir}/index.md`;
    const items = [];
    if (pages.includes(index)) {
      label = documentTitle(readFileSync(join(docs, index), "utf8"), join(docs, index));
      items.push({ slug: pageID(index) });
      placed.add(index);
    }
    const item = { label, items };
    groups.set(dir, item);
    return item;
  };
  // walk places the pages that the index.md of dir links to into items. A
  // link outside dir is skipped.
  const walk = (dir, items) => {
    const index = dir === "" ? "index.md" : `${dir}/index.md`;
    const file = join(docs, index);
    for (const link of sourceLinks(readFileSync(file, "utf8"))) {
      const target = linkTarget(settings, root, file, link.href);
      if (target.kind !== "page") continue;
      const page = path(target.file);
      if (placed.has(page) || (dir !== "" && !page.startsWith(`${dir}/`))) continue;
      if (basename(page) === "index.md") {
        const sub = page.slice(0, -"/index.md".length);
        const item = group(sub, sub);
        items.push(item);
        walk(sub, item.items);
      } else {
        placed.add(page);
        items.push({ slug: pageID(page) });
      }
    }
  };
  if (pages.includes("index.md")) {
    placed.add("index.md");
    top.push({ slug: pageID("index.md") });
    walk("", top);
  }
  // The remaining pages, in path order, go to the deepest group that holds
  // them. A directory between that group and the page becomes a group.
  for (const page of pages.filter((page) => !placed.has(page))) {
    if (placed.has(page)) continue;
    const parts = page.split("/").slice(0, -1);
    let depth = parts.length;
    while (depth > 0 && !groups.has(parts.slice(0, depth).join("/"))) depth--;
    let items = depth > 0 ? groups.get(parts.slice(0, depth).join("/")).items : top;
    for (let i = depth + 1; i <= parts.length; i++) {
      const item = group(parts.slice(0, i).join("/"), parts[i - 1]);
      items.push(item);
      items = item.items;
    }
    if (placed.has(page)) continue;
    placed.add(page);
    items.push({ slug: pageID(page) });
  }
  return top;
}

// links returns the elements "a" below the node, in the order of the document.
function links(node) {
  if (node.type !== "element") return [];
  if (node.tagName === "a") return [node];
  return (node.children ?? []).flatMap(links);
}

// pumlBlock returns the closed collapsible block that shows a diagram source.
function pumlBlock(summary, content) {
  return {
    type: "element",
    tagName: "details",
    properties: {},
    children: [
      { type: "element", tagName: "summary", properties: {}, children: [{ type: "text", value: summary }] },
      {
        type: "element",
        tagName: "pre",
        properties: {},
        children: [{ type: "element", tagName: "code", properties: { className: ["language-plantuml"] }, children: [{ type: "text", value: content }] }],
      },
    ],
  };
}

// requirementsPlugin returns the factory of the hast plugin for the settings
// of the site. The factory reads the body that the parser reads and the data
// of the frontmatter that the loader reads.
export function requirementsPlugin(settings, root) {
  const table = idTable(settings, root);
  const docs = join(root, settings.docsDir);
  const requirements = join(root, settings.requirementsDir);
  const valid = idPattern(settings.prefix, false);
  return (doc) => defineHastPlugin({
    name: "cumin-trace-requirements",
    // The positions of the nodes find the heading that gave the title.
    options: { position: true },
    // The first heading that became the title of the page leaves the body,
    // so that the title shows once (CTR-SITE-002). Each requirement becomes
    // one block (CTR-SITE-006).
    after(tree, ctx) {
      if (!ctx.fileURL) return;
      const file = fileURLToPath(ctx.fileURL);
      if (!inside(docs, file)) return;
      let children = [...tree.children];
      const isRequirement = (node) =>
        settings.prefix && inside(requirements, file) && requirementID(node, ctx, settings.prefix) !== undefined;
      if (doc.data?.astro?.frontmatter?.title === undefined) {
        // The heading on the line of the title leaves; a requirement heading
        // stays, with its anchor and its block.
        const { line } = firstHeading(doc.source, file);
        const first = children.findIndex((child) => headingLevel(child) > 0 && child.position?.start.line === line);
        if (first >= 0 && !isRequirement(children[first])) children.splice(first, 1);
      }
      if (settings.prefix && inside(requirements, file)) {
        children = blocks(children, isRequirement);
      }
      ctx.replaceNode(tree, { type: "root", children });
    },
    element: {
      filter: headingTags,
      visit(node, ctx) {
        if (!settings.prefix || !ctx.fileURL) return;
        if (!inside(requirements, fileURLToPath(ctx.fileURL))) return;
        const id = requirementID(node, ctx, settings.prefix);
        if (id === undefined) return;
        if (valid.test(id)) ctx.setProperty(node, "id", id);
        const code = node.children.find((child) => child.type === "element" && child.tagName === "code");
        ctx.setProperty(code, "className", ["requirement-id"]);
      },
    },
    text(node, ctx) {
      if (!settings.prefix) return;
      const refs = references(settings.prefix, node.value).filter(([from, to]) => table.has(node.value.slice(from, to)));
      if (refs.length === 0) return;
      for (let parent = ctx.parent(node); parent && parent.type !== "root"; parent = ctx.parent(parent)) {
        if (parent.type === "element" && ["a", "pre", "script", "style"].includes(parent.tagName)) return;
        if (isStatus(parent, ctx)) return;
        if (requirementID(parent, ctx, settings.prefix) !== undefined) return;
      }
      const parts = [];
      let at = 0;
      for (const [from, to] of refs) {
        if (from > at) parts.push({ type: "text", value: node.value.slice(at, from) });
        const id = node.value.slice(from, to);
        parts.push({
          type: "element",
          tagName: "a",
          properties: { href: `${table.get(id)}#${id}` },
          children: [{ type: "text", value: id }],
        });
        at = to;
      }
      if (at < node.value.length) parts.push({ type: "text", value: node.value.slice(at) });
      ctx.replaceNode(node, parts);
    },
  });
}

// linksPlugin returns the hast plugin that rewrites the relative links between
// documents (CTR-SITE-010). It runs in its own pass before requirementsPlugin,
// whose "after" hook replaces the whole tree.
export function linksPlugin(settings, root) {
  const docs = join(root, settings.docsDir);
  const placed = new WeakSet();
  return defineHastPlugin({
    name: "cumin-trace-links",
    element: {
      filter: ["a"],
      visit(node, ctx) {
        if (!ctx.fileURL) return;
        const file = fileURLToPath(ctx.fileURL);
        if (!inside(docs, file)) return;
        const target = linkTarget(settings, root, file, String(node.properties?.href ?? ""));
        if (target.kind === "keep") return;
        if (target.kind === "page") {
          ctx.setProperty(node, "href", target.url);
          return;
        }
        if (target.kind === "puml") {
          // The first link of a top-level block places the blocks of all the
          // diagram sources of that block after it, in the order of the links.
          let block = node;
          while (ctx.parent(block)?.type !== "root") block = ctx.parent(block);
          const all = links(block).filter((a) => linkTarget(settings, root, file, String(a.properties?.href ?? "")).kind === "puml");
          if (!placed.has(block)) {
            placed.add(block);
            ctx.insertAfter(
              block,
              all.map((a) => pumlBlock(ctx.textContent(a), readFileSync(linkTarget(settings, root, file, String(a.properties.href)).file, "utf8"))),
            );
          }
        }
        ctx.replaceNode(node, [{ type: "text", value: ctx.textContent(node) }]);
      },
    },
  });
}
