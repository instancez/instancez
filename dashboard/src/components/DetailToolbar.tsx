import { Link } from "react-router-dom";
import { ChevronLeft, Trash2 } from "lucide-react";
import { Box, HStack } from "@chakra-ui/react";
import { Button } from "./ui";

interface DetailToolbarProps {
  /** Label for the back link (the parent area's name, e.g. "Tables"). */
  backLabel: string;
  /** Delete handler for the entity being viewed. */
  onDelete: () => void;
}

/**
 * Slim in-content toolbar for detail pages: a back link on the left and a
 * delete button on the right. This is content (not page chrome), so it renders
 * natively wherever the page is mounted. Styles mirror PageHeader's old
 * back/delete affordances.
 */
export function DetailToolbar({ backLabel, onDelete }: DetailToolbarProps) {
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
      <Button variant="danger-outline" size="sm" onClick={onDelete}>
        <Trash2 size={14} />
        Delete
      </Button>
    </HStack>
  );
}
