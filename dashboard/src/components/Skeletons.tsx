import { Skeleton, VStack } from "@chakra-ui/react";

/** A list-row loading placeholder shaped like `ListRow` content. Used by areas
 *  that fetch their own data (Users, Functions deps) and by the platform host
 *  while it fetches the shared backend config on first open. */
export function ListSkeleton({ rows = 5 }: { rows?: number }) {
  // No `animate-rise` here: that's a dashboard-global CSS class that won't
  // resolve in the web bundle. Chakra's Skeleton pulses on its own.
  return (
    <VStack data-testid="list-skeleton" gap="2" align="stretch" pt="2">
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} height="14" borderRadius="lg" />
      ))}
    </VStack>
  );
}
