// The docs collection reads the documents under docs_dir where they are, and
// writes no copy (CTR-SITE-002, CTR-SITE-009).
import { readFileSync } from "node:fs";
import { join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { defineCollection } from "astro:content";
import { glob } from "astro/loaders";
import { docsSchema } from "@astrojs/starlight/schema";
import { body, firstHeading, pageID } from "./requirements.mjs";
import { readSettings, root } from "./settings.mjs";

const settings = readSettings();
// glob() takes base as a relative path or a URL. A file URL of the directory,
// with the last "/", keeps a "#" or a "?" of the path in the path.
const base = pathToFileURL(join(root, settings.docsDir, sep));
// A file path of the loader is relative to the project, site/, or absolute.
const project = fileURLToPath(new URL("..", import.meta.url));

// titled wraps glob(): when the frontmatter has no title, the first heading
// of the document becomes the title, before the schema checks the data. The
// rule of the first heading is shared with the plugin that removes it.
function titled(loader) {
  return {
    ...loader,
    load(context) {
      return loader.load({
        ...context,
        parseData(props) {
          if (props.data?.title === undefined && props.filePath) {
            const text = readFileSync(resolve(project, props.filePath), "utf8");
            props = { ...props, data: { ...props.data, title: firstHeading(body(text), props.filePath).title } };
          }
          return context.parseData(props);
        },
      });
    },
  };
}

// generateId gives each document the ID of pageID(), so that the links of the
// plugins go to the pages that Starlight builds.
export const collections = {
  docs: defineCollection({
    loader: titled(glob({ base, pattern: "**/*.md", generateId: ({ entry }) => pageID(entry) })),
    schema: docsSchema(),
  }),
};
