import type { ComponentType } from "react";
import type { MDXProps } from "mdx/types";

export interface DocMeta {
  title: string;
  description?: string;
  section?: string;
  order?: number;
}

export interface DocPage {
  slug: string;
  meta: DocMeta;
  Component: ComponentType<MDXProps>;
}

// Glob-import all .mdx files from content/
const modules = import.meta.glob<{
  default: ComponentType<MDXProps>;
  frontmatter: DocMeta;
}>("./content/*.mdx", { eager: true });

export const pages: DocPage[] = Object.entries(modules)
  .map(([path, mod]) => {
    const filename = path.replace("./content/", "").replace(".mdx", "");
    const slug = filename === "index" ? "" : filename;
    return {
      slug,
      meta: mod.frontmatter ?? { title: filename },
      Component: mod.default,
    };
  })
  .sort((a, b) => (a.meta.order ?? 999) - (b.meta.order ?? 999));

// Group pages by section for sidebar
export function groupBySection(
  docs: DocPage[]
): { section: string; pages: DocPage[] }[] {
  const groups = new Map<string, DocPage[]>();
  for (const page of docs) {
    const section = page.meta.section ?? "Guide";
    if (!groups.has(section)) groups.set(section, []);
    groups.get(section)!.push(page);
  }
  return Array.from(groups.entries()).map(([section, pages]) => ({
    section,
    pages,
  }));
}
