import { PencilLine } from "lucide-react";
import { Box, HStack, Text } from "@chakra-ui/react";
import type { ConfigStatus } from "../lib/types";

type Props = { status: ConfigStatus | null };

export function EditModeBanner({ status }: Props) {
  if (!status || status.dashboard_mode !== "readwrite") return null;
  return (
    <Box
      role="status"
      borderTopWidth="1px"
      borderColor="blue.200"
      bg="blue.50"
      _dark={{ bg: "blue.950", borderColor: "blue.800", color: "blue.100" }}
      px="4"
      py="2.5"
      fontSize="sm"
      color="blue.900"
    >
      <HStack as="span" alignItems="start" gap="2" display="inline-flex">
        <PencilLine size={14} aria-hidden="true" style={{ marginTop: "2px", flexShrink: 0 }} />
        <Text as="span">
          <Box as="strong">Live edit mode.</Box>{" "}
          Changes you make here are written directly to{" "}
          <Box as="code" fontFamily="mono">{status.config_source}</Box>{" "}
          and applied to the database. If your team manages{" "}
          <Box as="code" fontFamily="mono">instancez.yaml</Box> in git, mirror these changes there —
          anything written here will be overwritten the next time the source is updated outside
          the dashboard.
        </Text>
      </HStack>
    </Box>
  );
}
