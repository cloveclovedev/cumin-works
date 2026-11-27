// Reads .cumin-trace.toml at the top of the repository, the parent of site/
// or the given directory, and fills in the defaults (CTR-SITE-002). The
// format check of cumin-trace checks the values; the site does not. It also decides the name of the site
// (CTR-SITE-013).
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "smol-toml";

export const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

export function readSettings(top = root) {
  const file = join(top, ".cumin-trace.toml");
  const data = existsSync(file) ? parse(readFileSync(file, "utf8")) : {};
  const site = data.site ?? {};
  return {
    prefix: data.prefix ?? "",
    requirementsDir: data.requirements_dir ?? "docs/ja/requirements",
    docsDir: site.docs_dir ?? "docs/ja",
    base: site.base ?? "/",
    name: site.name ?? "",
  };
}

// siteName returns the name of the site: the name of the settings, then the
// name of the repository of origin, then the prefix (CTR-SITE-013).
export function siteName(settings, root) {
  if (settings.name) return settings.name;
  let url = "";
  try {
    url = execFileSync("git", ["config", "--get", "remote.origin.url"], { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
  } catch {
    // No git, no repository, or no origin: the prefix names the site.
  }
  const name = url.trim().replace(/\/+$/, "").replace(/\.git$/, "").split(/[/:]/).pop();
  return name || settings.prefix || "Requirements";
}
