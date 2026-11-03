import { Lock } from "lucide-react";
import { Box, HStack, Text } from "@chakra-ui/react";
import type { ConfigStatus } from "../lib/types";

export function ReadOnlyBanner({ status }: { status: ConfigStatus | null }) {
  if (!status?.read_only) return null;
  return (
    <Box
      role="status"
      borderTopWidth="1px"
      borderColor="orange.200"
      bg="orange.50"
      _dark={{ bg: "orange.950", borderColor: "orange.800", color: "orange.100" }}
      px="4"
      py="2.5"
      fontSize="sm"
      color="orange.900"
    >
      <HStack as="span" alignItems="start" gap="2" display="inline-flex">
        <Lock size={14} aria-hidden="true" style={{ marginTop: "2px", flexShrink: 0 }} />
        <Text as="span">
          <Box as="strong">Read-only.</Box>{" "}
          <Box as="code" fontFamily="mono">instancez.yaml</Box> uses <Box as="code" fontFamily="mono">include:</Box>, so
          the dashboard cannot save it. Edit the YAML files instead.
        </Text>
      </HStack>
    </Box>
  );
}
