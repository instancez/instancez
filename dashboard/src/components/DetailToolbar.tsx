import { Link } from "react-router-dom";
import { ChevronLeft, Trash2 } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { Button } from "./ui";

interface DetailToolbarProps {
  /** Label for the back link (the parent area's name, e.g. "Tables"). */
  backLabel: string;
}

/** In-content back link for detail pages; deletion lives in DangerZone. */
export function DetailToolbar({ backLabel }: DetailToolbarProps) {
  return (
    <HStack justify="space-between" gap="4" mb="6">
      <Box
        asChild
        display="inline-flex"
        alignItems="center"
        gap="1.5"
        px="3"
        py="1.5"
        borderRadius="lg"
        fontSize="sm"
        fontWeight="medium"
        borderWidth="1px"
        color="fg.muted"
        _hover={{ color: "fg", bg: "bg.subtle" }}
        transition="colors"
      >
        <Link to=".." relative="path">
          <ChevronLeft size={14} />
          {backLabel}
        </Link>
      </Box>
    </HStack>
  );
}

/** Bottom-of-page section holding the irreversible delete action for the entity being viewed. */
export function DangerZone({ title, description, label, onDelete }: { title: string; description: string; label: string; onDelete: () => void }) {
  return (
    <Box mt="2" pb="8">
      <HStack justify="space-between" gap="4" px="5" py="4" borderWidth="1px" borderColor="border.error" borderRadius="xl">
        <VStack align="start" gap="0.5">
          <Text fontSize="sm" fontWeight="semibold" color="fg">{title}</Text>
          <Text fontSize="xs" color="fg.muted">{description}</Text>
        </VStack>
        <Button variant="danger-outline" size="sm" onClick={onDelete}>
          <Trash2 size={14} />
          {label}
        </Button>
      </HStack>
    </Box>
  );
}
