import type { ComponentPropsWithoutRef } from "react";
import type { MDXComponents } from "mdx/types";
import { Callout } from "./Callout";

function slugify(text: unknown): string | undefined {
  if (typeof text !== "string") return undefined;
  return text
    .toLowerCase()
    .replace(/\s+/g, "-")
    .replace(/[^\w-]/g, "");
}

function HeadingWithAnchor(
  Tag: "h2" | "h3",
  props: ComponentPropsWithoutRef<"h2">
) {
  const id = slugify(props.children);
  return (
    <Tag id={id} {...props}>
      {props.children}
      {id && (
        <a href={`#${id}`} className="anchor-link" aria-label={`Link to ${props.children}`}>
          #
        </a>
      )}
    </Tag>
  );
}

export const mdxComponents: MDXComponents = {
  table: (props: ComponentPropsWithoutRef<"table">) => (
    <div className="doc-table-wrapper">
      <table {...props} />
    </div>
  ),

  h2: (props: ComponentPropsWithoutRef<"h2">) => HeadingWithAnchor("h2", props),
  h3: (props: ComponentPropsWithoutRef<"h3">) => HeadingWithAnchor("h3", props),

  Callout,
};
