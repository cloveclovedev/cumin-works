// The site of the requirement documents, with the default look of Starlight
// (CTR-SITE-001). It builds static files only (CTR-SITE-008). The default
// Markdown processor, Sätteri, adds the anchors and the links of the
// requirement IDs (CTR-SITE-003) and the block of each requirement
// (CTR-SITE-006). The one style sheet draws the blocks, and sets the width of
// the body and the corners of its images (CTR-SITE-014). A first plugin
// rewrites the relative links between documents (CTR-SITE-010). Astro shows
// each SVG next to a document from its relative path (CTR-SITE-004), and a
// script in the head opens a diagram on a click (CTR-SITE-007). The name at
// the top left comes from siteName() (CTR-SITE-013). The one root locale
// shows the UI in Japanese, and adds no language directory to the URLs
// (CTR-SITE-012). The sidebar of sidebar() follows the links of the entry
// document (CTR-SITE-011), in place of the default sidebar. The first item of
// the sidebar is the link to the dashboard page (CTR-DASH-005); Starlight adds
// base to the link.
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import { satteri } from "@astrojs/markdown-satteri";
import { readSettings, root, siteName } from "./src/settings.mjs";
import { diagramScript, linksPlugin, requirementsPlugin, sidebar } from "./src/requirements.mjs";

const settings = readSettings();

export default defineConfig({
  base: settings.base,
  markdown: {
    processor: satteri({ hastPlugins: [linksPlugin(settings, root), requirementsPlugin(settings, root)] }),
  },
  integrations: [
    starlight({
      title: siteName(settings, root),
      locales: { root: { label: "日本語", lang: "ja" } },
      sidebar: [{ label: "ダッシュボード", link: "/dashboard/" }, ...sidebar(settings, root)],
      customCss: ["./src/requirements.css"],
      head: [{ tag: "script", content: diagramScript }],
    }),
  ],
});
