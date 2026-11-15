import { useEffect, useLayoutEffect, useRef } from "react";
import { X } from "lucide-react";
import { Box, HStack, Portal, Text } from "@chakra-ui/react";

// Only the top-most overlay reacts to Escape, so a confirm over a modal closes just itself.
const overlayStack: symbol[] = [];

interface OverlayProps {
  onDismiss: () => void;
  /** Backdrop clicks are ignored while the content has unsaved input. */
  dismissOnBackdrop?: boolean;
  visible?: boolean;
  children: React.ReactNode;
}

/** Shared backdrop for Modal and the DialogProvider variants: Escape, backdrop click, first-field focus. */
export function Overlay({ onDismiss, dismissOnBackdrop = true, visible = true, children }: OverlayProps) {
  const ref = useRef<HTMLDivElement>(null);
  const dismiss = useRef(onDismiss);
  dismiss.current = onDismiss;

  useLayoutEffect(() => {
    const id = Symbol("overlay");
    overlayStack.push(id);
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape" || overlayStack[overlayStack.length - 1] !== id) return;
      e.stopPropagation();
      dismiss.current();
    }
    window.addEventListener("keydown", onKey, true);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      overlayStack.splice(overlayStack.indexOf(id), 1);
    };
  }, []);

  useEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const first = ref.current?.querySelector<HTMLElement>("input:not([readonly]), select, textarea")
      ?? ref.current?.querySelector<HTMLElement>("button, [href], [tabindex]:not([tabindex='-1'])");
    first?.focus();
    return () => {
      if (opener?.isConnected) opener.focus();
    };
  }, []);

  return (
    <Portal>
      <Box
        ref={ref}
        position="fixed"
        inset="0"
        zIndex="overlay"
        display="flex"
        alignItems="center"
        justifyContent="center"
        bg={visible ? "blackAlpha.600" : "transparent"}
        backdropFilter={visible ? "blur(2px)" : undefined}
        transition="all 0.2s ease-out"
        data-testid="modal-backdrop"
        onClick={(e) => {
          if (dismissOnBackdrop && e.target === e.currentTarget) onDismiss();
        }}
      >
        {children}
      </Box>
    </Portal>
  );
}

interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  /** Set while the form has unsaved input so a stray backdrop click can't discard it. */
  dirty?: boolean;
  children: React.ReactNode;
}

function ModalRoot({ open, onClose, title, dirty, children }: ModalProps) {
  if (!open) return null;

  return (
    <Overlay onDismiss={onClose} dismissOnBackdrop={!dirty}>
      <Box
        role="dialog"
        aria-modal="true"
        aria-label={title}
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
    </Overlay>
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
