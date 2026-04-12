import { MDXProvider } from "@mdx-js/react";
import { Link } from "react-router-dom";
import { ArrowLeft, ArrowRight } from "lucide-react";
import type { DocPage } from "../content";
import { pages } from "../content";
import { mdxComponents } from "./MDXComponents";

interface Props {
  page: DocPage;
}

export function DocPageView({ page }: Props) {
  const { Component, meta } = page;

  const idx = pages.findIndex((p) => p.slug === page.slug);
  const prev = idx > 0 ? pages[idx - 1] : null;
  const next = idx < pages.length - 1 ? pages[idx + 1] : null;

  return (
    <>
      <article className="prose">
        {meta.description && (
          <p className="text-base text-muted-foreground mb-2 !mt-0">{meta.description}</p>
        )}
        <h1>{meta.title}</h1>
        <MDXProvider components={mdxComponents}>
          <Component />
        </MDXProvider>
      </article>

      {/* Prev / Next */}
      {(prev || next) && (
        <nav className="mt-16 pt-8 border-t border-border grid grid-cols-2 gap-4">
          {prev ? (
            <Link
              to={prev.slug === "" ? "/" : `/${prev.slug}`}
              className="group flex flex-col gap-1.5 p-4 rounded-xl border border-border hover:border-accent/30 hover:bg-surface/50 transition-all cursor-pointer"
            >
              <span className="flex items-center gap-1.5 text-xs text-muted-foreground group-hover:text-accent transition-colors">
                <ArrowLeft size={12} />
                Previous
              </span>
              <span className="text-sm font-medium text-foreground group-hover:text-accent transition-colors">
                {prev.meta.title}
              </span>
            </Link>
          ) : (
            <div />
          )}
          {next ? (
            <Link
              to={`/${next.slug}`}
              className="group flex flex-col items-end gap-1.5 p-4 rounded-xl border border-border hover:border-accent/30 hover:bg-surface/50 transition-all cursor-pointer text-right"
            >
              <span className="flex items-center gap-1.5 text-xs text-muted-foreground group-hover:text-accent transition-colors">
                Next
                <ArrowRight size={12} />
              </span>
              <span className="text-sm font-medium text-foreground group-hover:text-accent transition-colors">
                {next.meta.title}
              </span>
            </Link>
          ) : (
            <div />
          )}
        </nav>
      )}
    </>
  );
}
