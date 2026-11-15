import { useEffect, useLayoutEffect, useRef } from "react";
import { X } from "lucide-react";
import { Box, HStack, Portal, Text } from "@chakra-ui/react";
import { Button } from "./ui";

// Only the top-most overlay reacts to Escape, so a confirm over a modal closes just itself.
const overlayStack: symbol[] = [];

interface OverlayProps {
  onDismiss: () => void;
  /** Backdrop clicks are ignored while the content has unsaved input. */
  dismissOnBackdrop?: boolean;
  visible?: boolean;
  children: React.ReactNode;
}

/** Backdrop behind every dialog: Escape, backdrop click, first-field focus, focus restore. */
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

export interface DialogAction {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  loading?: boolean;
}

export interface DialogFrameProps {
  title: string;
  /** Muted line under the title. */
  description?: React.ReactNode;
  /** `danger` only recolors the primary button; the layout never changes. */
  tone?: "default" | "danger";
  /** Dialog width: md is 480px, lg is 672px for content like diffs. */
  size?: "md" | "lg";
  /** Blocks every way of closing while an action runs. */
  busy?: boolean;
  /** Inline error at the bottom of the body. */
  error?: React.ReactNode;
  primary?: DialogAction;
  /** Defaults to Cancel, which closes. Pass `false` for a single-action dialog. */
  secondary?: DialogAction | false;
  /** Extra footer content on the left. */
  footerStart?: React.ReactNode;
  /** Set while the form has unsaved input so a stray backdrop click can't discard it. */
  dirty?: boolean;
  visible?: boolean;
  onClose: () => void;
  children?: React.ReactNode;
}

/** The one dialog shell for the console: header, body, footer, close button. Change structure here only. */
export function DialogFrame({
  title,
  description,
  tone = "default",
  size = "md",
  busy = false,
  error,
  primary,
  secondary,
  footerStart,
  dirty,
  visible = true,
  onClose,
  children,
}: DialogFrameProps) {
  const close = () => {
    if (!busy) onClose();
  };

  return (
    <Overlay onDismiss={close} dismissOnBackdrop={!dirty} visible={visible}>
      <Box
        role={tone === "danger" ? "alertdialog" : "dialog"}
        aria-modal="true"
        aria-label={title}
        position="relative"
        w="full"
        maxW={size === "lg" ? "672px" : "480px"}
        mx="4"
        borderRadius="2xl"
        overflow="hidden"
        borderWidth="1px"
        borderColor="border"
        bg="bg.panel"
        boxShadow="lg"
        opacity={visible ? 1 : 0}
        transform={visible ? "scale(1) translateY(0)" : "scale(0.95) translateY(8px)"}
        transition="all 0.2s ease-out"
      >
        <Box px="6" pt="5" pb={children || error ? "3" : "4"} pr="12">
          <Text fontSize="md" fontWeight="semibold" color="fg">{title}</Text>
          {description && <Text fontSize="sm" color="fg.muted" mt="1">{description}</Text>}
        </Box>
        <Box
          as="button"
          onClick={close}
          {...({ disabled: busy } as object)}
          aria-label="Close"
          position="absolute"
          top="4"
          right="4"
          p="1.5"
          borderRadius="lg"
          color="fg.muted"
          _hover={{ color: "fg", bg: "bg.subtle" }}
          cursor={busy ? "not-allowed" : "pointer"}
        >
          <X size={14} />
        </Box>
        {(children || error) && (
          <Box px="6" pb="4" display="flex" flexDirection="column" gap="3" maxH="60vh" overflowY="auto">
            {children}
            {error && <Text role="alert" fontSize="sm" color="fg.error">{error}</Text>}
          </Box>
        )}
        {(primary || secondary !== false || footerStart) && (
          <HStack px="6" pb="5" pt="1" gap="2.5" justify={footerStart ? "space-between" : "flex-end"}>
            {footerStart}
            <HStack gap="2.5">
              {secondary !== false && (
                <Button variant="outline" onClick={secondary?.onClick ?? close} disabled={busy || secondary?.disabled}>
                  {secondary?.label ?? "Cancel"}
                </Button>
              )}
              {primary && (
                <Button
                  variant={tone === "danger" ? "danger" : "primary"}
                  onClick={primary.onClick}
                  loading={primary.loading ?? busy}
                  disabled={primary.disabled}
                >
                  {primary.label}
                </Button>
              )}
            </HStack>
          </HStack>
        )}
      </Box>
    </Overlay>
  );
}
