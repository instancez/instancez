import { useEffect } from "react";
import { X } from "lucide-react";
import { Box, HStack, Portal, Text } from "@chakra-ui/react";

interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
}

function ModalRoot({ open, onClose, title, children }: ModalProps) {
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <Portal>
      <Box
        position="fixed"
        inset="0"
        zIndex="overlay"
        display="flex"
        alignItems="center"
        justifyContent="center"
        bg="blackAlpha.600"
        backdropFilter="blur(2px)"
        data-testid="modal-backdrop"
        onClick={(e) => {
          if (e.target === e.currentTarget) onClose();
        }}
      >
        <Box
          w="full"
          maxW="480px"
          mx="4"
          borderRadius="2xl"
          overflow="hidden"
          borderWidth="1px"
          borderColor="border"
          bg="bg.panel"
          boxShadow="lg"
        >
          <HStack
            justify="space-between"
            px="6"
            pt="5"
            pb="4"
            borderBottomWidth="1px"
            borderColor="border"
          >
            <Text fontSize="md" fontWeight="semibold" color="fg">
              {title}
            </Text>
            <Box
              as="button"
              onClick={onClose}
              aria-label="Close"
              p="1"
              borderRadius="md"
              color="fg.muted"
              _hover={{ color: "fg", bg: "bg.subtle" }}
              cursor="pointer"
            >
              <X size={14} />
            </Box>
          </HStack>
          {children}
        </Box>
      </Box>
    </Portal>
  );
}

function ModalBody({ children }: { children: React.ReactNode }) {
  return (
    <Box px="6" py="4">
      {children}
    </Box>
  );
}

function ModalFooter({ children }: { children: React.ReactNode }) {
  return (
    <HStack
      justify="flex-end"
      gap="2.5"
      px="6"
      pb="5"
      pt="3"
      borderTopWidth="1px"
      borderColor="border"
    >
      {children}
    </HStack>
  );
}

export const Modal = Object.assign(ModalRoot, {
  Body: ModalBody,
  Footer: ModalFooter,
});
