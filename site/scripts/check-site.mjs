// Builds the site and checks the HTML of the output. It prints one line for
// each problem and ends with the exit code 1 when one exists.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { basename, dirname, extname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { readSettings, root } from "../src/settings.mjs";
import { atxHeadings, body as markdownBody, bodyLines, diagramScript, documentTitle, dotPath, escape, firstHeading, hasTitle, headingIDs, idTable, linkTarget, pageURL, sidebar, sourceLinks } from "../src/requirements.mjs";
import { areaTitle, discrepancies, issueURL, japanTime, readTrace, requirementURL, tree } from "../src/dashboard.mjs";

const site = join(dirname(fileURLToPath(import.meta.url)), "..");
const dist = join(site, "dist");
const settings = readSettings();
const problems = [];
const docs = join(root, settings.docsDir);
// CTR-SITE-013: the name that each page shows at the top left. The argument
// --site-name wins over the name of the settings. With neither, the check of
// the name does not run.
const { values: args } = parseArgs({ options: { "site-name": { type: "string" } } });
const wantedName = args["site-name"] ?? settings.name;

// digest returns the SHA-256 of a file, or undefined when no file is there.
function digest(file) {
  return existsSync(file) && statSync(file).isFile() ? createHash("sha256").update(readFileSync(file)).digest("hex") : undefined;
}

// digests returns the SHA-256 of each file under docs_dir, by its path.
function digests() {
  const found = new Map();
  for (const entry of existsSync(docs) ? readdirSync(docs, { recursive: true, withFileTypes: true }) : []) {
    if (!entry.isFile()) continue;
    const file = join(entry.parentPath, entry.name);
    found.set(relative(root, file), digest(file));
  }
  return found;
}

// Astro reuses the rendered documents of its cache, so the check removes the
// cache and the old output, and builds from the documents of now.
for (const dir of [dist, join(site, ".astro"), join(site, "node_modules", ".astro")]) rmSync(dir, { recursive: true, force: true });
const before = digests();
const build = spawnSync(join(site, "node_modules", ".bin", "astro"), ["build"], { cwd: site, stdio: "inherit" });
if (build.error) console.log(`astro build: ${build.error.message}`);
else if (build.status !== 0) console.log(`astro build: ended with ${build.status === null ? `the signal ${build.signal}` : `the exit code ${build.status}`}`);
if (build.error || build.status !== 0) process.exit(build.status || 1);

// CTR-SITE-002: the build leaves each file under docs_dir as it was.
const after = digests();
for (const name of new Set([...before.keys(), ...after.keys()])) {
  if (before.get(name) !== after.get(name)) problems.push(`${name}: the build changed the file`);
}

// pageFile returns the path of the output page of a URL under base.
function pageFile(url) {
  return join(dist, url.slice(settings.base.length), "index.html");
}

// page returns the HTML of the output page of a URL under base, or undefined.
function page(url) {
  const file = pageFile(url);
  return existsSync(file) ? readFileSync(file, "utf8") : undefined;
}

const id = `${settings.prefix}-[A-Z0-9]+-[0-9]{3}(?:\\.[1-9][0-9]*)?`;

// CTR-SITE-008: the site has no server functions, only static files. A
// directory of documents named "server" or "functions" puts their pages in
// the output directory of that name, so such a directory may hold the pages
// of documents, and no other file.
const config = readFileSync(join(site, "astro.config.mjs"), "utf8");
if (/\badapter\s*:/.test(config)) problems.push("site/astro.config.mjs: the config has an adapter, want static files only");
if (/\boutput\s*:\s*["'`]server["'`]/.test(config)) problems.push('site/astro.config.mjs: the config has output "server", want static files only');
if (existsSync(join(dist, "_worker.js"))) problems.push("site/dist/_worker.js: the output has a server function, want static files only");
const documentPages = new Set();
const dashboardURL = `${settings.base}dashboard/`;
const dashboardFile = pageFile(dashboardURL);
for (const entry of existsSync(docs) ? readdirSync(docs, { recursive: true, withFileTypes: true }) : []) {
  const path = relative(docs, join(entry.parentPath, entry.name));
  if (entry.isFile() && entry.name.endsWith(".md") && !dotPath(path)) documentPages.add(pageFile(pageURL(settings.base, path)));
}
for (const name of ["_functions", "functions", "server"]) {
  const dir = join(dist, name);
  if (!existsSync(dir)) continue;
  const files = statSync(dir).isDirectory() ? readdirSync(dir, { recursive: true, withFileTypes: true }).filter((e) => !e.isDirectory()).map((e) => join(e.parentPath, e.name)) : [dir];
  const others = files.filter((file) => !documentPages.has(file));
  if (files.length === 0 || others.length > 0) {
    problems.push(`site/dist/${name}: the output has a server function, want static files only: ${others.map((file) => relative(dist, file)).join(", ") || "an empty directory"}`);
  }
}

// CTR-SITE-012: each page is the page of a document at pageURL(), or the
// dashboard page, with no directory above docs_dir and no language
// directory. Each page of a document
// has lang "ja", and shows the search, the table of contents, and the
// pagination in Japanese.
for (const file of readdirSync(dist, { recursive: true, withFileTypes: true })) {
  const path = join(file.parentPath, file.name);
  if (file.isFile() && file.name === "index.html" && !documentPages.has(path) && path !== dashboardFile) {
    problems.push(`site/dist/${relative(dist, path)}: the page is at no URL of pageURL()`);
  }
}
for (const path of documentPages) {
  if (!existsSync(path)) continue;
  const html = readFileSync(path, "utf8");
  const url = `${settings.base}${relative(dist, dirname(path)).split(sep).join("/")}`;
  const ui = [
    [/<html lang="ja"[\s>]/, '<html lang="ja">'],
    [/<site-search [^>]*>\s*<button [^>]*aria-label="検索"/, 'search button "検索"'],
  ];
  if (html.includes('id="starlight__on-this-page')) ui.push([/<h2 id="starlight__on-this-page"[^>]*>目次<\/h2>/, 'table of contents "目次"']);
  if (/<a [^>]*rel="prev"/.test(html)) ui.push([/<a [^>]*rel="prev"[^>]*>[\s\S]*?<span[^>]*>前へ<br/, 'previous link "前へ"']);
  if (/<a [^>]*rel="next"/.test(html)) ui.push([/<a [^>]*rel="next"[^>]*>[\s\S]*?<span[^>]*>次へ<br/, 'next link "次へ"']);
  for (const [pattern, want] of ui) {
    if (!pattern.test(html)) problems.push(`${url}: the page has no ${want}`);
  }
}

// CTR-SITE-011: the sidebar of the page of the entry document starts with
// the link to the dashboard page, then lists the pages of sidebar() in its
// order, and each page of a document once. After the dashboard link, the top
// level starts with the page of the entry document, then follows the order of
// the links of the entry document. CTR-SITE-012: no item or group of the top
// level is named after a part of docs_dir, unless a directory of the
// documents has that name.
{
  const entry = join(docs, "index.md");
  const html = page(settings.base) ?? "";
  const shown = sidebarItems(html);
  const all = shown.filter((item) => !item.group);
  if (all[0]?.url !== decoded(dashboardURL)) problems.push(`${settings.base}: the sidebar starts with ${all[0]?.url}, want the dashboard page ${dashboardURL}`);
  const links = all.slice(1);
  const flat = (items) => items.flatMap((item) => (item.items ? flat(item.items) : [decoded(`${settings.base}${item.slug === "index" ? "" : `${item.slug}/`}`)]));
  const want = flat(sidebar(settings, root));
  if (links.map((item) => item.url).join("\n") !== want.join("\n")) {
    problems.push(`${settings.base}: the sidebar lists ${JSON.stringify(links.map((item) => item.url))}, want the items of sidebar() ${JSON.stringify(want)}`);
  }
  for (const path of documentPages) {
    const url = decoded(`${settings.base}${relative(dist, dirname(path)).split(sep).join("/")}${dirname(path) === dist ? "" : "/"}`);
    const count = links.filter((item) => item.url === url).length;
    if (count !== 1) problems.push(`${url}: the page shows ${count} times in the sidebar, want 1`);
  }
  // The first item of each item of the top level: the link, or the first
  // link of the group.
  const firsts = [];
  for (let i = 0; i < shown.length; i++) {
    if (shown[i].depth !== 1) continue;
    const first = shown[i].group ? shown.slice(i + 1).find((item) => !item.group) : shown[i];
    if (first !== undefined) firsts.push(first.url);
  }
  // The first item of the top level is the dashboard link, checked above.
  firsts.shift();
  if (existsSync(entry)) {
    if (firsts[0] !== decoded(settings.base)) problems.push(`${settings.base}: the sidebar starts with ${firsts[0]}, want the page of the entry document`);
    const linked = [];
    for (const link of sourceLinks(readFileSync(entry, "utf8"))) {
      const target = linkTarget(settings, root, entry, link.href);
      const url = target.kind === "page" ? decoded(target.url.split("#")[0]) : undefined;
      if (url !== undefined && url !== decoded(settings.base) && !linked.includes(url)) linked.push(url);
    }
    const order = firsts.slice(1).filter((url) => linked.includes(url));
    const wantOrder = linked.filter((url) => order.includes(url));
    if (order.length === 0 && linked.length > 0) problems.push(`${settings.base}: the top level of the sidebar has none of the links of the entry document`);
    if (order.join("\n") !== wantOrder.join("\n")) {
      problems.push(`${settings.base}: the top level of the sidebar lists ${JSON.stringify(order)}, want the order of the entry document ${JSON.stringify(wantOrder)}`);
    }
  }
  const names = new Set(readdirSync(docs, { withFileTypes: true }).filter((e) => e.isDirectory()).map((e) => e.name));
  for (const item of shown.filter((item) => item.depth === 1 && item.url !== decoded(dashboardURL))) {
    const part = settings.docsDir.split("/").find((part) => part !== "" && part === item.label && !names.has(part));
    if (part !== undefined) problems.push(`${settings.base}: the top level of the sidebar has ${JSON.stringify(item.label)}, a directory above the documents`);
  }
}

// sidebarItems returns the items of the sidebar of a page, in the order of
// the page: the URL and the label of each link, and the label of each
// group, with the depth of its list (1 for the top level).
function sidebarItems(html) {
  const start = html.indexOf('<ul class="top-level');
  if (start === -1) return [];
  const items = [];
  let depth = 0;
  for (const match of html.slice(start).matchAll(/<ul[\s>]|<\/ul>|<a href="([^"]*)"[^>]*>([\s\S]*?)<\/a>|<span class="group-label[^"]*">([\s\S]*?)<\/span><\/span>/g)) {
    if (match[0].startsWith("<ul")) depth++;
    else if (match[0] === "</ul>") depth--;
    else if (match[1] !== undefined) items.push({ depth, url: decoded(match[1]), label: rawText(match[2].replace(/<[^>]*>/g, "")) });
    else items.push({ depth, group: true, label: rawText(match[3].replace(/<[^>]*>/g, "")) });
    if (depth === 0) break;
  }
  return items;
}

// marks returns the text with no white space and no marks that Markdown can
// remove: "*", "_", "`", "~", and "\\".
function marks(text) {
  return text.replace(/[\s*_`~\\]/g, "");
}

// headingText returns the text of the Markdown of a heading as it reads in
// the HTML, for marks(): a link or an image keeps only its text.
function headingText(markdown) {
  return marks(markdown.replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1"));
}

// sourceHeadings returns the text of each heading of a document, for
// marks(): the ATX headings, the setext headings, and the HTML headings. The
// page shows all three kinds as <h1> to <h6>, so the check counts them alike.
function sourceHeadings(text) {
  const found = atxHeadings(markdownBody(text)).map((heading) => headingText(heading.text));
  const lines = bodyLines(text);
  // A setext heading is a paragraph that a line of "=" or "-" ends. A list
  // item, a quote, an ATX heading, or a table row is no such paragraph.
  let paragraph = [];
  for (const line of lines) {
    if (/^ {0,3}(=+|-+)[ \t]*$/.test(line) && paragraph.length > 0) {
      found.push(headingText(paragraph.join(" ")));
      paragraph = [];
    } else if (line.trim() === "" || /^ {0,3}(#{1,6}([ \t]|$)|>|[-+*][ \t]|[0-9]{1,9}[.)][ \t]|\|)/.test(line)) {
      paragraph = [];
    } else if (paragraph.length > 0 || !/^ {4}/.test(line)) {
      paragraph.push(line);
    }
  }
  const html = lines.join("\n").replace(/`[^`]*`/g, "");
  for (const match of html.matchAll(/<h([1-6])(?:\s[^>]*)?>([\s\S]*?)<\/h\1\s*>/gi)) found.push(marks(plainText(match[2])));
  return found;
}

// CTR-SITE-002: the <title> of each page starts with the title of its
// document. A page whose title comes from its first heading shows the title
// once, in one <h1>, and its body does not repeat the heading. A page
// with a title in its frontmatter keeps its first heading in the body.
for (const entry of existsSync(docs) ? readdirSync(docs, { recursive: true, withFileTypes: true }) : []) {
  if (!entry.isFile() || !entry.name.endsWith(".md")) continue;
  const file = join(entry.parentPath, entry.name);
  const text = readFileSync(file, "utf8");
  const html = page(pageURL(settings.base, relative(docs, file)));
  if (html === undefined) continue;
  // The <title> starts with the title of the document, then the site name.
  // A <title> holds text and entities, and no tags.
  const want = documentTitle(text, file);
  const pageTitle = html.match(/<title>([\s\S]*?)<\/title>/)?.[1];
  if (want === "" || !rawText(pageTitle ?? "").startsWith(want.replace(/\s+/g, ""))) {
    problems.push(`${relative(root, file)}: the <title> of the page does not start with the title ${JSON.stringify(want)}`);
  }
  const content = body(html);
  const headings = [...content.matchAll(/<h[1-6][^>]*>([\s\S]*?)<\/h[1-6]>/g)].map((m) => marks(plainText(m[1])));
  const { title, line } = firstHeading(markdownBody(text), file);
  const sources = sourceHeadings(text).filter((heading) => heading === headingText(title)).length;
  const shown = headings.filter((heading) => heading === headingText(title)).length;
  if (hasTitle(text)) {
    if (line > 0 && shown < sources) problems.push(`${relative(root, file)}: the body has ${shown} headings "${title}", want ${sources}`);
    continue;
  }
  // A requirement heading of level 1 is no title, and it stays in the body.
  const count = html.match(new RegExp(`<h1(?! id="${id}")[\\s>]`, "g"))?.length ?? 0;
  if (count !== 1) problems.push(`${relative(root, file)}: the page has ${count} <h1>, want 1`);
  if (line > 0 && headingIDs(settings.prefix, atxHeadings(markdownBody(text))[0].source).length === 0 && shown > sources - 1) {
    problems.push(`${relative(root, file)}: the body repeats the title "${title}"`);
  }
}

// CTR-SITE-003: each requirement heading has an anchor whose name is its ID.
// CTR-SITE-006: each requirement heading starts its own block, and the ID is
// an element apart from the title.
const pages = new Map();
const requirements = join(root, settings.requirementsDir);
for (const entry of existsSync(requirements) ? readdirSync(requirements, { recursive: true, withFileTypes: true }) : []) {
  if (!entry.isFile() || !entry.name.endsWith(".md")) continue;
  const file = join(entry.parentPath, entry.name);
  if (dotPath(relative(docs, file))) continue;
  const url = pageURL(settings.base, relative(docs, file));
  const html = page(url);
  if (html === undefined) {
    problems.push(`${relative(root, file)}: no page at ${url}`);
    continue;
  }
  pages.set(url, html);
  for (const id of headingIDs(settings.prefix, readFileSync(file, "utf8"))) {
    if (!new RegExp(`<h[1-6][^>]* id="${escape(id)}"`).test(html)) {
      problems.push(`${relative(root, file)}: the heading of ${id} has no anchor ${id}`);
    }
    if (!new RegExp(`<section class="requirement"><h[1-6][^>]* id="${escape(id)}"[^>]*><code class="requirement-id">${escape(id)}</code>`).test(html)) {
      problems.push(`${relative(root, file)}: ${id} is not a block that starts with its heading and its ID apart`);
    }
  }
  for (const match of html.matchAll(/<section class="requirement">(?!<h[1-6])/g)) {
    problems.push(`${relative(root, file)}: a block of a requirement starts with no heading, at ${match.index}`);
  }
}

// body returns the Markdown content of a page with no HTML comments: from the
// content element to the pagination links of Starlight.
function body(html) {
  // The class also stands in the script of the head; the body is the div.
  const start = html.indexOf('<div class="sl-markdown-content"');
  if (start === -1) return "";
  const end = html.indexOf('class="pagination-links', start);
  return html.slice(start, end === -1 ? undefined : end).replace(/<!--[\s\S]*?-->/g, "");
}

// CTR-SITE-003: each link to a requirement ID on a page of a document goes to
// a page under base that has the anchor, and no link stands inside a code
// block.
for (const entry of existsSync(docs) ? readdirSync(docs, { recursive: true, withFileTypes: true }) : []) {
  if (!entry.isFile() || !entry.name.endsWith(".md")) continue;
  const url = pageURL(settings.base, relative(docs, join(entry.parentPath, entry.name)));
  const html = body(page(url) ?? "");
  for (const match of html.matchAll(new RegExp(`<a href="([^"#]+)#(${id})"`, "g"))) {
    const [, target, anchor] = match;
    const html = target.startsWith(settings.base) ? page(target) : undefined;
    if (html === undefined) {
      problems.push(`${url}: the link to ${anchor} goes to ${target}, which is no page under ${settings.base}`);
    } else if (!html.includes(` id="${anchor}"`)) {
      problems.push(`${url}: the link to ${anchor} goes to ${target}, which has no anchor ${anchor}`);
    }
  }
  for (const block of html.matchAll(/<pre[\s>][\s\S]*?<\/pre>/g)) {
    if (block[0].includes("<a ")) problems.push(`${url}: a code block holds a link`);
  }
}

// decoded returns the URL with no percent-encoding, or the URL itself when it
// holds a malformed one.
function decoded(url) {
  try {
    return decodeURI(url);
  } catch {
    return url;
  }
}

// plainText returns the text of an HTML fragment with no tags, no entities,
// and no white space.
function plainText(html) {
  return rawText(html.replace(/<[^>]*>/g, ""));
}

// rawText returns the text of an element that holds no tags, such as
// <title>, with no entities and no white space.
function rawText(html) {
  const entities = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " " };
  return html
    .replace(/&(#x[0-9a-f]+|#[0-9]+|[a-z]+);/gi, (all, name) => {
      if (name[0] === "#") return String.fromCodePoint(name[1] === "x" || name[1] === "X" ? parseInt(name.slice(2), 16) : parseInt(name.slice(1), 10));
      return entities[name] ?? all;
    })
    .replace(/\s+/g, "");
}

// imagePaths returns the relative paths of the images of a document, outside
// the frontmatter, the code blocks, the code spans, and the HTML comments.
function imagePaths(text) {
  const paths = [];
  for (const line of bodyLines(text)) {
    for (const match of line.replace(/`[^`]*`/g, "").matchAll(/!\[[^\]]*\]\(<?([^)\s>]+)/g)) {
      if (!URL.canParse(match[1]) && !match[1].startsWith("/")) paths.push(match[1]);
    }
  }
  return paths;
}

// CTR-SITE-010: each relative link of a document goes to the page of the
// document and its heading, shows a diagram source in a closed collapsible
// block, or keeps only its text. A link to a missing page or heading is a
// broken link. CTR-SITE-004: each image that a document links to stands in
// its page, and the file of the image is in the output. CTR-SITE-007: each
// page has the script that opens a diagram on a click.
let documents = 0;
let images = 0;
for (const entry of existsSync(docs) ? readdirSync(docs, { recursive: true, withFileTypes: true }) : []) {
  if (!entry.isFile() || !entry.name.endsWith(".md")) continue;
  const file = join(entry.parentPath, entry.name);
  if (dotPath(relative(docs, file))) continue;
  const url = pageURL(settings.base, relative(docs, file));
  const html = page(url);
  const name = relative(root, file);
  if (html === undefined) {
    problems.push(`${name}: no page at ${url}`);
    continue;
  }
  documents++;
  if (wantedName) {
    const shown = html.match(/<a [^>]*class="site-title[ "][^>]*>([\s\S]*?)<\/a>/)?.[1];
    if (shown === undefined || plainText(shown) !== wantedName.replace(/\s+/g, "")) {
      problems.push(`${name}: the page ${url} shows the site name ${JSON.stringify(shown === undefined ? "" : plainText(shown))}, want ${JSON.stringify(wantedName)}`);
    }
  }
  const content = body(html);
  const hrefs = new Set([...content.matchAll(/<a [^>]*href="([^"]*)"/g)].map((m) => decoded(m[1])));
  if ([...hrefs].some((href) => !URL.canParse(href) && !href.startsWith("/") && /\.puml(#.*)?$/.test(href))) {
    problems.push(`${name}: the page ${url} has a relative link to a .puml file`);
  }
  const blocks = [...content.matchAll(/<details>([\s\S]*?)<\/details>/g)].map((m) => m[1]);
  if (!html.includes(`<script>${diagramScript}</script>`)) {
    problems.push(`${name}: the page ${url} has no script that opens a diagram`);
  }
  const sources = [...content.matchAll(/<img [^>]*src="([^"]+)"/g)].map((m) => m[1]);
  for (const path of imagePaths(readFileSync(file, "utf8"))) {
    // Astro writes the file as <name>.<hash><extension>, and a raster image
    // as <name>.<hash>.webp. Images with the same bytes share one file under
    // the name of one of them. Each image takes its own <img>.
    const extension = extname(decoded(path));
    const extensions = extension.toLowerCase() === ".svg" ? escape(extension) : `(?:${escape(extension)}|\\.webp)`;
    const output = new RegExp(`^${escape(basename(decoded(path), extension))}\\.[A-Za-z0-9_-]+${extensions}$`);
    const bytes = digest(resolve(dirname(file), decoded(path)));
    let index = sources.findIndex((src) => output.test(decoded(basename(src))));
    if (index === -1 && bytes !== undefined) {
      index = sources.findIndex((src) => src.startsWith(settings.base) && digest(join(dist, decoded(src).slice(settings.base.length))) === bytes);
    }
    const source = index === -1 ? undefined : sources.splice(index, 1)[0];
    if (source === undefined) {
      problems.push(`${name}: the page ${url} shows no image ${path}`);
    } else if (!source.startsWith(settings.base) || !existsSync(join(dist, decoded(source).slice(settings.base.length)))) {
      problems.push(`${name}: the image ${path} goes to ${source}, which is no file under ${settings.base}`);
    } else {
      images++;
    }
  }
  for (const link of sourceLinks(readFileSync(file, "utf8"))) {
    const target = linkTarget(settings, root, file, link.href);
    if (target.kind === "page") {
      const [path, hash] = target.url.split("#");
      const html = page(path);
      if (!hrefs.has(decoded(target.url))) {
        problems.push(`${name}: the link to ${link.href} does not go to ${target.url}`);
      } else if (html === undefined) {
        problems.push(`${name}: the link to ${link.href} is broken: no page at ${path}`);
      } else if (hash !== undefined && !html.includes(` id="${decoded(hash)}"`)) {
        problems.push(`${name}: the link to ${link.href} is broken: ${path} has no heading #${hash}`);
      }
    } else if (target.kind === "puml") {
      const source = plainText(readFileSync(target.file, "utf8").replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[c]));
      if (!blocks.some((block) => plainText(block).includes(source))) {
        problems.push(`${name}: the link to ${link.href} has no closed collapsible block with the content of the file`);
      }
    } else if (target.kind === "text" && hrefs.has(decoded(link.href))) {
      problems.push(`${name}: the link to ${link.href} is still a link, want only its text`);
    }
  }
}

// CTR-DASH-001, CTR-DASH-005: the dashboard page is in the output, and the
// first link of the sidebar of each page goes to it. CTR-DASH-006: without a
// valid site/trace.json, the body of the page is only the sentence of the
// failed read; with one, the <time> of the page has read_at as its datetime
// and japanTime() of read_at as its text. CTR-DASH-001: the map of tiles and
// the list have the categories of tree() with the titles of areaTitle(), and
// one tile and one line for each requirement row of tree(), in its order and
// with its values, and no deprecated requirement; the section has no table
// and no details element. CTR-DASH-005: each requirement link goes to a page
// with its anchor, and each issue link is issueURL(). CTR-DASH-009: between
// the map and the list, one line for each discrepancy of discrepancies(), in
// its order, with its kind, its issue link, and its ID; no heading when there
// is none. CTR-DASH-007: the output holds no trace.json.
{
  const html = page(dashboardURL);
  if (html === undefined) problems.push(`${dashboardURL}: no dashboard page`);
  else if (html.includes('id="starlight__on-this-page')) problems.push(`${dashboardURL}: the page has a table of contents, want none`);
  for (const path of [...documentPages, dashboardFile]) {
    if (!existsSync(path)) continue;
    const first = sidebarItems(readFileSync(path, "utf8")).find((item) => !item.group);
    if (first?.url !== decoded(dashboardURL)) problems.push(`site/dist/${relative(dist, path)}: the first link of the sidebar goes to ${first?.url}, want ${dashboardURL}`);
  }
  for (const file of readdirSync(dist, { recursive: true, withFileTypes: true })) {
    if (file.name === "trace.json") problems.push(`site/dist/${relative(dist, join(file.parentPath, file.name))}: the output holds the trace`);
  }
  // The body ends before the footer of Starlight.
  const content = body(html ?? "").split("<footer")[0];
  const trace = readTrace(root);
  if (html === undefined) {
    // The problem is above.
  } else if (trace === undefined) {
    if (plainText(content) !== plainText("GitHub を読めなかったため、進捗を表示できません。") || content.includes("<table")) {
      problems.push(`${dashboardURL}: with no valid site/trace.json, the body is not only the sentence of the failed read`);
    }
  } else {
    const time = content.match(/<p class="dashboard-read-at">[\s\S]*?<time datetime="([^"]*)">([\s\S]*?)<\/time>/);
    if (time?.[1] !== trace.read_at) problems.push(`${dashboardURL}: the datetime of the read time is ${time?.[1]}, want ${trace.read_at}`);
    if (time === null || rawText(time[2]) !== japanTime(trace.read_at).replace(/\s+/g, "")) problems.push(`${dashboardURL}: the page shows the read time ${time?.[2]}, want ${japanTime(trace.read_at)}`);
    const table = idTable(settings, root);
    const want = tree(trace);
    const categories = want.filter((row) => row.kind === "category");
    const requirements = want.filter((row) => row.kind === "requirement");
    const ids = (items) => JSON.stringify(items.map((item) => item.id));
    const hrefs = (html) => [...html.matchAll(/<a [^>]*href="([^"]*)"/g)].map((m) => decoded(m[1]));
    const attrs = (tag) => Object.fromEntries([...tag.matchAll(/([a-z-]+)="([^"]*)"/g)].map((m) => [m[1], m[2]]));
    if (content.includes("<table") || content.includes("<details")) problems.push(`${dashboardURL}: the progress of the requirements has a table or a details element, want the tiles and the lines`);
    // The map: one row for each category, then one tile for each requirement.
    const mapRows = [...content.matchAll(/<div class="dashboard-map-row"([^>]*)>[\s\S]*?<b>([\s\S]*?)<\/b>[\s\S]*?<span class="dashboard-count">([\s\S]*?)<\/span>/g)].map((m) => ({ id: attrs(m[1])["data-id"], title: plainText(m[2]), count: plainText(m[3]) }));
    const tiles = [...content.matchAll(/<(?:a|span) class="dashboard-tile"([^>]*)>/g)].map((m) => { const a = attrs(m[1]); return { id: a["data-id"], state: a["data-state"], link: a.href === undefined ? undefined : decoded(a.href), label: rawText(a["aria-label"] ?? "") }; });
    // The list: a header for each category, then one line for each requirement.
    const groups = [...content.matchAll(/<section class="dashboard-group"([^>]*)>[\s\S]*?<h3>([\s\S]*?)<small>[\s\S]*?<span class="dashboard-count">([\s\S]*?)<\/span>/g)].map((m) => ({ id: attrs(m[1])["data-id"], title: plainText(m[2]), count: plainText(m[3]) }));
    const lines = [...content.matchAll(/<div class="dashboard-line"([^>]*)>([\s\S]*?)<\/div>/g)].map((m) => {
      const inner = m[2];
      const [, issues = "", implementation = ""] = inner.match(/<span class="dashboard-issues">([\s\S]*?)<span class="dashboard-implementation">([\s\S]*)$/) ?? [];
      return {
        id: attrs(m[1])["data-id"],
        state: attrs(m[1])["data-state"],
        progress: plainText(inner.match(/<span class="sr-only">([\s\S]*?)<\/span>/)?.[1] ?? ""),
        link: inner.match(/<a class="dashboard-id" href="([^"]*)"/)?.[1],
        issues: hrefs(issues.split('<span class="dashboard-milestone"')[0]).join(" "),
        milestones: [...issues.matchAll(/<span class="dashboard-milestone">([\s\S]*?)<\/span>/g)].map((s) => plainText(s[1])).join("、"),
        implementation: plainText(implementation.match(/<span class="dashboard-count">([\s\S]*?)<\/span>/)?.[1] ?? ""),
        subIssues: [...implementation.matchAll(/<a class="dashboard-square"([^>]*)>/g)].map((s) => { const a = attrs(s[1]); return `${decoded(a.href)} ${a["data-closed"]}`; }).join(" "),
      };
    });
    for (const [name, got] of [["map", mapRows], ["list", groups]]) {
      if (ids(got) !== ids(categories)) problems.push(`${dashboardURL}: the ${name} has the categories ${ids(got)}, want ${ids(categories)}`);
      for (const [i, item] of got.entries()) {
        const category = categories[i];
        if (category === undefined || category.id !== item.id) break;
        const title = plainText(areaTitle(settings, root, category));
        if (item.title !== title) problems.push(`${dashboardURL}: the ${name} names the category ${item.id} ${JSON.stringify(item.title)}, want ${JSON.stringify(title)}`);
        if (!item.count.endsWith(category.progress.replace(/\s+/g, ""))) problems.push(`${dashboardURL}: the ${name} shows ${JSON.stringify(item.count)} for the category ${item.id}, want ${JSON.stringify(category.progress)}`);
      }
    }
    for (const [name, got] of [["map", tiles], ["list", lines]]) {
      if (ids(got) !== ids(requirements)) problems.push(`${dashboardURL}: the ${name} has the requirements ${ids(got)}, want the rows of tree() ${ids(requirements)}`);
    }
    for (const [i, tile] of tiles.entries()) {
      const wanted = requirements[i];
      if (wanted === undefined || wanted.id !== tile.id) break;
      if (tile.state !== wanted.state) problems.push(`${dashboardURL}: the tile of ${tile.id} has the progress ${tile.state}, want ${wanted.state}`);
      // The label is the ID, the title, the progress, and the implementation
      // issues closed / total.
      const label = `${wanted.id} ${wanted.title} ・ ${wanted.progress}${wanted.total > 0 ? ` ・ 実装Issue ${wanted.closed} / ${wanted.total}` : ""}`.replace(/\s+/g, "");
      if (tile.label !== label) problems.push(`${dashboardURL}: the tile of ${tile.id} reads ${JSON.stringify(tile.label)}, want ${JSON.stringify(label)}`);
    }
    for (const [i, line] of lines.entries()) {
      const wanted = requirements[i];
      if (wanted === undefined || wanted.id !== line.id) break;
      const expected = {
        state: wanted.state,
        progress: wanted.progress.replace(/\s+/g, ""),
        issues: wanted.issues.map((number) => issueURL(trace.repository, number)).join(" "),
        milestones: wanted.milestones.join("、"),
        implementation: wanted.implementation.replace(/\s+/g, ""),
        subIssues: wanted.subIssues.map((sub) => `${issueURL(sub.repository, sub.number)} ${sub.closed}`).join(" "),
      };
      for (const key of Object.keys(expected)) {
        if (line[key] !== expected[key]) problems.push(`${dashboardURL}: the line of ${line.id} shows ${key} ${JSON.stringify(line[key])}, want ${JSON.stringify(expected[key])}`);
      }
    }
    // Each requirement link of a tile and of a line goes to a page with its
    // anchor; an ID that no document has is no link.
    for (const item of [...tiles, ...lines]) {
      const link = item.link === undefined ? undefined : decoded(item.link);
      const target = requirementURL(table, item.id);
      if (link !== (target === undefined ? undefined : decoded(target))) {
        problems.push(`${dashboardURL}: the requirement ${item.id} links to ${link}, want ${target}`);
      } else if (link !== undefined) {
        const [path, anchor] = link.split("#");
        const html = path.startsWith(settings.base) ? page(path) : undefined;
        if (html === undefined || !html.includes(` id="${anchor}"`)) problems.push(`${dashboardURL}: the link of ${item.id} goes to ${link}, which is no page with the anchor ${anchor}`);
      }
    }
    for (const requirement of trace.requirements.filter((r) => r.status === "deprecated")) {
      if ([...tiles, ...lines].some((item) => item.id === requirement.id)) problems.push(`${dashboardURL}: the page shows the deprecated requirement ${requirement.id}`);
    }
    // The discrepancies: a heading after the map and before the list, then
    // one line for each, or no heading and no line when there is none.
    const wantProblems = discrepancies(trace);
    const heading = content.search(/<h2[^>]*>\s*不整合\s*<\/h2>/);
    const map = content.indexOf('<div class="dashboard-map"');
    const list = content.indexOf('<section class="dashboard-group"');
    const problemLines = [...content.matchAll(/<div class="dashboard-discrepancy"([^>]*)>([\s\S]*?)<\/div>/g)].map((m) => {
      const a = attrs(m[1]);
      const inner = m[2];
      const id = inner.match(/<(a|code) class="dashboard-id"(?: href="([^"]*)")?>([\s\S]*?)<\/(?:a|code)>/);
      return {
        kind: a["data-kind"],
        label: plainText(inner.match(/<span class="dashboard-title"[^>]*>([\s\S]*?)<\/span>/)?.[1] ?? ""),
        issue: hrefs(inner.match(/<a class="dashboard-issue"[^>]*>/)?.[0] ?? "").join(" "),
        id: id === null ? undefined : plainText(id[3]),
        link: id?.[2] === undefined ? undefined : decoded(id[2]),
        at: m.index,
      };
    });
    if (wantProblems.length === 0) {
      if (heading !== -1 || problemLines.length > 0) problems.push(`${dashboardURL}: the trace has no discrepancy, but the page shows the discrepancies`);
    } else {
      if (heading === -1 || !(map < heading && (list === -1 || heading < list))) problems.push(`${dashboardURL}: the heading of the discrepancies is not between the map and the list`);
      if (problemLines.some((line) => line.at < heading || (list !== -1 && line.at > list))) problems.push(`${dashboardURL}: a discrepancy is not between its heading and the list`);
      const show = (items) => JSON.stringify(items.map((item) => [item.kind, item.label, item.issue, item.id, item.link]));
      const want = wantProblems.map((d) => {
        const target = requirementURL(table, d.id);
        return { kind: d.kind, label: plainText(d.label), issue: decoded(issueURL(trace.repository, d.issue)), id: d.id, link: target === undefined ? undefined : decoded(target) };
      });
      if (show(problemLines) !== show(want)) problems.push(`${dashboardURL}: the discrepancies are ${show(problemLines)}, want ${show(want)}`);
    }
  }
}

for (const problem of problems) console.log(problem);
if (problems.length > 0) process.exit(1);
console.log(`check-site: ${pages.size} pages of requirements, ${documents} pages of documents, and ${images} images checked`);
