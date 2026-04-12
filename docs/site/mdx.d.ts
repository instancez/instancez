declare module "*.mdx" {
  import type { MDXProps } from "mdx/types";

  export const frontmatter: {
    title: string;
    description?: string;
    section?: string;
    order?: number;
  };

  export default function MDXContent(props: MDXProps): JSX.Element;
}
