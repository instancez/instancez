import { Box, Text, VStack } from "@chakra-ui/react";

interface DiffViewerProps {
  statements: string[];
  isDestructive: boolean;
}

export function DiffViewer({ statements, isDestructive }: DiffViewerProps) {
  if (statements.length === 0) {
    return (
      <Box borderRadius="xl" borderWidth="1px" bg="bg.subtle" p="4">
        <Text fontSize="sm" color="fg.muted" textAlign="center">No pending migrations</Text>
      </Box>
    );
  }

  return (
    <Box borderRadius="xl" borderWidth="1px" bg="bg.subtle" overflow="hidden">
      {isDestructive && (
        <Box
          px="4"
          py="2"
          bg="red.50"
          _dark={{ bg: "red.900/20" }}
          borderBottomWidth="1px"
          borderColor="red.200"
        >
          <Text fontSize="xs" fontWeight="medium" color="fg.error">
            Warning: This migration contains destructive operations
          </Text>
        </Box>
      )}
      <VStack gap="2" p="4" overflowX="auto" align="stretch">
        {statements.map((stmt, i) => {
          const isDrop = /DROP|DELETE/i.test(stmt);
          const isAdd = /ADD|CREATE/i.test(stmt);
          return (
            <Box
              key={i}
              as="pre"
              fontSize="xs"
              fontFamily="mono"
              p="2"
              borderRadius="md"
              whiteSpace="pre-wrap"
              lineHeight="relaxed"
              bg={isDrop ? "red.50" : isAdd ? "green.50" : "bg.muted"}
              color={isDrop ? "fg.error" : isAdd ? "green.700" : "fg"}
              _dark={{
                bg: isDrop ? "red.900/30" : isAdd ? "green.900/30" : "bg.muted",
                color: isDrop ? "red.300" : isAdd ? "green.300" : "fg",
              }}
            >
              {stmt}
            </Box>
          );
        })}
      </VStack>
    </Box>
  );
}
