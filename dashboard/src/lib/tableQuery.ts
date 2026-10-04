export const DATA_PAGE_SIZE = 50;

const quoteIdent = (id: string) => `"${id.replace(/"/g, '""')}"`;

/** Fetches one extra row so the caller can tell if a next page exists. */
export function pageQuery(table: string, orderBy: string | undefined, page: number): string {
  // No primary key: order by the first column so OFFSET paging stays stable.
  const order = ` ORDER BY ${orderBy ? quoteIdent(orderBy) : "1"}`;
  return `SELECT * FROM ${quoteIdent(table)}${order} LIMIT ${DATA_PAGE_SIZE + 1} OFFSET ${page * DATA_PAGE_SIZE}`;
}
