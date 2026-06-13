import logoUrl from "../assets/instancez-logo-only.svg";
import { Box, HStack, Text } from "@chakra-ui/react";

interface LogoProps {
  size?: number;
  className?: string;
}

export function Logo({ size = 36, className }: LogoProps) {
  const classes = ["dark:invert", className].filter(Boolean).join(" ");
  return (
    <img
      src={logoUrl}
      width={size}
      height={size}
      alt="instancez"
      className={classes || undefined}
    />
  );
}

/** Brand lockup used in the navbar and on the login card. */
export function Wordmark({ badge = "Dashboard" }: { badge?: string }) {
  return (
    <HStack as="span" display="inline-flex" alignItems="center" gap="2">
      <Logo size={26} />
      <Text as="span" fontSize="xl" fontWeight="bold" color="fg">instancez</Text>
      <Box
        as="span"
        position="relative"
        top="px"
        ml="1"
        px="2"
        py="0.5"
        borderRadius="md"
        fontSize="10px"
        fontWeight="bold"
        textTransform="uppercase"
        letterSpacing="0.05em"
        bg="fg"
        color="bg"
        borderWidth="1px"
        boxShadow="xs"
      >
        {badge}
      </Box>
    </HStack>
  );
}
