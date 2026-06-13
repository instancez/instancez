import React from "react";
import type { LucideIcon } from "lucide-react";
import { Box, Center, Text, VStack } from "@chakra-ui/react";

interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  description: string;
  action?: React.ReactNode;
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
}: EmptyStateProps) {
  return (
    <Center
      flexDir="column"
      py="16"
      px="4"
      textAlign="center"
      className="animate-rise"
    >
      <VStack gap="0">
        <Box
          w="12"
          h="12"
          borderRadius="xl"
          bg="bg.muted"
          borderWidth="1px"
          display="flex"
          alignItems="center"
          justifyContent="center"
          mb="4"
        >
          <Icon size={22} strokeWidth={1.5} color="var(--chakra-colors-fg-muted)" />
        </Box>
        <Text fontSize="sm" fontWeight="semibold" color="fg">{title}</Text>
        <Text mt="1" fontSize="sm" color="fg.muted" maxW="sm">
          {description}
        </Text>
        {action && <Box mt="4">{action}</Box>}
      </VStack>
    </Center>
  );
}
