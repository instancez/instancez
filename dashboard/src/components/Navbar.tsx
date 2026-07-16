import { Box, HStack, IconButton, Link } from "@chakra-ui/react";
import { Github } from "lucide-react";
import { Wordmark } from "./Logo";
import { ColorModeButton } from "./color-mode";

/**
 * Floating pill navbar, matching the coder app's shell: a bordered
 * rounded-xl surface inset from the viewport edges with the brand lockup
 * on the left and quick actions on the right.
 */
export function Navbar() {
  return (
    <Box
      as="header"
      flexShrink="0"
      m="2"
      px="4"
      py="2"
      display="flex"
      alignItems="center"
      justifyContent="space-between"
      bg="bg.panel"
      borderWidth="1px"
      borderRadius="xl"
      boxShadow="xs"
    >
      <Wordmark />
      <HStack gap="1">
        <Link
          href="https://github.com/instancez/instancez"
          target="_blank"
          rel="noopener noreferrer"
          aria-label="GitHub repository"
        >
          <IconButton
            variant="ghost"
            size="sm"
            colorPalette="gray"
            aria-label="GitHub repository"
          >
            <Github size={16} />
          </IconButton>
        </Link>
        <ColorModeButton />
      </HStack>
    </Box>
  );
}
