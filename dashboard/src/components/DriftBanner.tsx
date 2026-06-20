import { TriangleAlert } from "lucide-react";
import { Box, HStack, Text } from "@chakra-ui/react";
import type { ConfigStatus } from "../lib/types";

type Props = { status: ConfigStatus | null };

export function DriftBanner({ status }: Props) {
  if (!status || status.status !== "drift") return null;
  return (
    <Box
      role="alert"
      borderTopWidth="1px"
      borderColor="orange.300"
      bg="orange.50"
      _dark={{ bg: "orange.950", borderColor: "orange.800" }}
      px="4"
      py="2.5"
      fontSize="sm"
      color="fg"
    >
      <HStack as="span" alignItems="start" gap="2" display="inline-flex">
        <TriangleAlert size={14} aria-hidden="true" style={{ marginTop: "2px", flexShrink: 0, color: "var(--chakra-colors-orange-600)" }} />
        <Text as="span">
          <Box as="strong">Configuration drift.</Box>{" "}
          The source <Box as="code" fontFamily="mono">{status.config_source}</Box> has changes that
          failed to apply: <Box as="code" fontFamily="mono">{status.last_error}</Box>. The server is
          running on the last successful config from{" "}
          <Box as="time" {...({ dateTime: status.running.applied_at } as object)}>{status.running.applied_at}</Box>.{" "}
          Fix the source and restart, or revert the failing change.
        </Text>
      </HStack>
    </Box>
  );
}
