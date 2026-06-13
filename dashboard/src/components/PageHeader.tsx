import React from "react";
import { Box, HStack, Text } from "@chakra-ui/react";

interface PageHeaderProps {
  title: string;
  description?: string;
  actions?: React.ReactNode;
}

/**
 * Shell-owned page chrome: title + optional description and actions. Rendered
 * once by Layout from the matched route's handle — pages themselves are
 * chrome-free, so this no longer carries page-level back/delete affordances
 * (those moved into page content as DetailToolbar).
 */
export function PageHeader({ title, description, actions }: PageHeaderProps) {
  return (
    <HStack justify="space-between" gap="4" px="8" pt="8" pb="6" mb="2" align="start">
      <Box minW="0">
        <Text fontSize="2xl" fontWeight="bold" letterSpacing="tight" color="fg" truncate>
          {title}
        </Text>
        {description && (
          <Text mt="1.5" fontSize="sm" color="fg.muted">{description}</Text>
        )}
      </Box>
      {actions && <HStack gap="2" flexShrink="0">{actions}</HStack>}
    </HStack>
  );
}
