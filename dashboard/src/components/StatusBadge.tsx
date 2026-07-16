import React from "react";
import { Box, HStack } from "@chakra-ui/react";

type Variant = "success" | "error" | "warning" | "info" | "muted";

const VARIANT_STYLES: Record<Variant, { borderColor: string; bg: string; color: string }> = {
  success: { borderColor: "green.300", bg: "green.50", color: "green.700" },
  error:   { borderColor: "red.300",   bg: "red.50",   color: "red.600"  },
  warning: { borderColor: "orange.300", bg: "orange.50", color: "orange.700" },
  info:    { borderColor: "blue.300",  bg: "blue.50",  color: "blue.700" },
  muted:   { borderColor: "border",    bg: "bg.muted", color: "fg.muted" },
};

const DOT_COLOR: Record<Variant, string> = {
  success: "green.500",
  error:   "red.500",
  warning: "orange.500",
  info:    "blue.500",
  muted:   "fg.muted",
};

interface StatusBadgeProps {
  variant: Variant;
  children: React.ReactNode;
  className?: string;
  dot?: boolean;
}

export function StatusBadge({
  variant,
  children,
  className,
  dot = false,
}: StatusBadgeProps) {
  const styles = VARIANT_STYLES[variant];
  return (
    <HStack
      as="span"
      display="inline-flex"
      alignItems="center"
      gap="1.5"
      px="2"
      py="0.5"
      borderRadius="md"
      borderWidth="1px"
      fontSize="xs"
      fontWeight="medium"
      borderColor={styles.borderColor}
      bg={styles.bg}
      color={styles.color}
      className={className}
    >
      {dot && (
        <Box
          as="span"
          w="1.5"
          h="1.5"
          borderRadius="full"
          bg={DOT_COLOR[variant]}
          flexShrink="0"
        />
      )}
      {children}
    </HStack>
  );
}
