import { createContext, useContext, useState, useRef, useEffect, useCallback } from "react";
import { X, AlertTriangle, Info, Trash2 } from "lucide-react";
import { Box, HStack, Input, Portal, Text, VStack } from "@chakra-ui/react";
import { Button, Field, Select } from "./ui";

type DialogType = "prompt" | "confirm" | "alert" | "select";

interface DialogState {
  type: DialogType;
  title: string;
  message?: string;
  defaultValue?: string;
  placeholder?: string;
  confirmLabel?: string;
  confirmText?: string;
  destructive?: boolean;
  options?: string[];
  resolve: (value: any) => void;
}

interface DialogContextValue {
  prompt: (title: string, options?: { message?: string; defaultValue?: string; placeholder?: string }) => Promise<string | null>;
  confirm: (title: string, options?: { message?: string; confirmLabel?: string; destructive?: boolean; confirmText?: string }) => Promise<boolean>;
  alert: (title: string, options?: { message?: string }) => Promise<void>;
  select: (title: string, options: string[], extra?: { message?: string }) => Promise<string | null>;
}

const DialogContext = createContext<DialogContextValue | null>(null);

export function useDialog() {
  const ctx = useContext(DialogContext);
  if (!ctx) throw new Error("useDialog must be used within DialogProvider");
  return ctx;
}

export function DialogProvider({ children }: { children: React.ReactNode }) {
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const [inputValue, setInputValue] = useState("");
  const [confirmInput, setConfirmInput] = useState("");
  const [selectValue, setSelectValue] = useState("");
  const [visible, setVisible] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const confirmInputRef = useRef<HTMLInputElement>(null);
  const selectRef = useRef<HTMLSelectElement>(null);

  const prompt = useCallback(
    (title: string, options?: { message?: string; defaultValue?: string; placeholder?: string }) =>
      new Promise<string | null>((resolve) => {
        setInputValue(options?.defaultValue || "");
        setDialog({ type: "prompt", title, message: options?.message, defaultValue: options?.defaultValue, placeholder: options?.placeholder, resolve });
      }),
    []
  );

  const confirm = useCallback(
    (title: string, options?: { message?: string; confirmLabel?: string; destructive?: boolean; confirmText?: string }) =>
      new Promise<boolean>((resolve) => {
        setConfirmInput("");
        setDialog({
          type: "confirm",
          title,
          message: options?.message,
          confirmLabel: options?.confirmLabel,
          confirmText: options?.confirmText,
          destructive: options?.destructive ?? true,
          resolve,
        });
      }),
    []
  );

  const alert = useCallback(
    (title: string, options?: { message?: string }) =>
      new Promise<void>((resolve) => {
        setDialog({ type: "alert", title, message: options?.message, resolve: () => resolve() });
      }),
    []
  );

  const select = useCallback(
    (title: string, options: string[], extra?: { message?: string }) =>
      new Promise<string | null>((resolve) => {
        setSelectValue(options[0] || "");
        setDialog({ type: "select", title, message: extra?.message, options, resolve });
      }),
    []
  );

  function close(value: string | boolean | null) {
    setVisible(false);
    setTimeout(() => {
      dialog?.resolve(value);
      setDialog(null);
    }, 150);
  }

  function handleConfirm() {
    if (dialog?.type === "prompt") {
      close(inputValue.trim() || null);
    } else if (dialog?.type === "select") {
      close(selectValue || null);
    } else if (dialog?.type === "confirm") {
      close(true);
    } else {
      close(null);
    }
  }

  function handleCancel() {
    if (dialog?.type === "prompt") {
      close(null);
    } else if (dialog?.type === "confirm") {
      close(false);
    } else {
      close(null);
    }
  }

  const confirmLocked = dialog?.type === "confirm" && dialog.confirmText
    ? confirmInput !== dialog.confirmText
    : false;

  useEffect(() => {
    if (dialog) {
      requestAnimationFrame(() => setVisible(true));
      if (dialog.type === "prompt") {
        setTimeout(() => {
          inputRef.current?.focus();
          inputRef.current?.select();
        }, 50);
      } else if (dialog.type === "select") {
        setTimeout(() => selectRef.current?.focus(), 50);
      } else if (dialog.type === "confirm" && dialog.confirmText) {
        setTimeout(() => confirmInputRef.current?.focus(), 50);
      }
    }
  }, [dialog]);

  useEffect(() => {
    if (!dialog) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") handleCancel();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dialog]);

  return (
    <DialogContext.Provider value={{ prompt, confirm, alert, select }}>
      {children}
      {dialog && (
        <Portal>
          <Box
            position="fixed"
            inset="0"
            zIndex="overlay"
            display="flex"
            alignItems="center"
            justifyContent="center"
            bg={visible ? "blackAlpha.600" : "transparent"}
            backdropFilter={visible ? "blur(2px)" : undefined}
            transition="all 0.2s ease-out"
            onClick={(e) => { if (e.target === e.currentTarget) handleCancel(); }}
          >
            <Box
              position="relative"
              w="full"
              maxW="420px"
              mx="4"
              borderRadius="2xl"
              overflow="hidden"
              borderWidth="1px"
              borderColor={dialog.type === "confirm" && dialog.destructive ? "red.200" : "border"}
              bg="bg.panel"
              boxShadow="lg"
              opacity={visible ? 1 : 0}
              transform={visible ? "scale(1) translateY(0)" : "scale(0.95) translateY(8px)"}
              transition="all 0.2s ease-out"
            >
              {/* Close button */}
              <Box
                as="button"
                onClick={handleCancel}
                position="absolute"
                top="4"
                right="4"
                p="1.5"
                borderRadius="lg"
                color="fg.muted"
                _hover={{ color: "fg", bg: "bg.subtle" }}
                cursor="pointer"
                zIndex="1"
              >
                <X size={14} />
              </Box>

              {/* Prompt variant */}
              {dialog.type === "prompt" && (
                <>
                  <Box px="6" pt="6" pb="5">
                    <VStack gap="3" align="stretch">
                      <Field label={dialog.title} htmlFor="dialog-input">
                        <Input
                          ref={inputRef}
                          id="dialog-input"
                          type="text"
                          value={inputValue}
                          onChange={(e) => setInputValue(e.target.value)}
                          onKeyDown={(e) => { if (e.key === "Enter" && inputValue.trim()) handleConfirm(); }}
                          placeholder={dialog.placeholder || "Enter a name…"}
                          size="sm"
                        />
                      </Field>
                      {dialog.message && <Text fontSize="xs" color="fg.muted">{dialog.message}</Text>}
                    </VStack>
                  </Box>
                  <HStack justify="flex-end" gap="2.5" px="6" pb="5">
                    <Button variant="ghost" onClick={handleCancel}>Cancel</Button>
                    <Button onClick={handleConfirm} disabled={!inputValue.trim()}>Create</Button>
                  </HStack>
                </>
              )}

              {/* Select variant */}
              {dialog.type === "select" && (
                <>
                  <Box px="6" pt="6" pb="5">
                    <VStack gap="3" align="stretch">
                      <Field label={dialog.title} htmlFor="dialog-select">
                        <Select
                          value={selectValue}
                          onChange={(e) => setSelectValue((e.target as HTMLSelectElement).value)}
                          mono
                        >
                          {(dialog.options || []).map((opt) => (
                            <option key={opt} value={opt}>{opt}</option>
                          ))}
                        </Select>
                      </Field>
                      {dialog.message && <Text fontSize="xs" color="fg.muted">{dialog.message}</Text>}
                    </VStack>
                  </Box>
                  <HStack justify="flex-end" gap="2.5" px="6" pb="5">
                    <Button variant="ghost" onClick={handleCancel}>Cancel</Button>
                    <Button onClick={handleConfirm} disabled={!selectValue}>Select</Button>
                  </HStack>
                </>
              )}

              {/* Destructive confirm */}
              {dialog.type === "confirm" && dialog.destructive && (
                <>
                  <Box h="1.5" bg="red.500" />
                  <Box px="6" pt="5" pb="2">
                    <HStack gap="3.5" align="start">
                      <Box w="10" h="10" borderRadius="xl" bg="red.50" _dark={{ bg: "red.900/30" }} display="flex" alignItems="center" justifyContent="center" flexShrink="0">
                        <Box as={Trash2} boxSize="4.5" color="fg.error" />
                      </Box>
                      <Box minW="0" pt="0.5" flex="1">
                        <Text fontSize="md" fontWeight="semibold" color="fg" pr="8">{dialog.title}</Text>
                        {dialog.message && <Text fontSize="sm" color="fg.muted" mt="1.5">{dialog.message}</Text>}
                      </Box>
                    </HStack>
                  </Box>
                  {dialog.confirmText && (
                    <Box px="6" pt="3" pb="1">
                      <Field label={<>Type <Box as="span" fontFamily="mono" fontWeight="semibold" color="fg">{dialog.confirmText}</Box> to confirm</>} htmlFor="confirm-input">
                        <Input
                          ref={confirmInputRef}
                          id="confirm-input"
                          value={confirmInput}
                          onChange={(e) => setConfirmInput(e.target.value)}
                          onKeyDown={(e) => { if (e.key === "Enter" && !confirmLocked) handleConfirm(); }}
                          placeholder={dialog.confirmText}
                          spellCheck={false}
                          autoComplete="off"
                          size="sm"
                          fontFamily="mono"
                        />
                      </Field>
                    </Box>
                  )}
                  <Box mx="6" mt="4" px="3.5" py="2.5" borderRadius="lg" bg="red.50" _dark={{ bg: "red.900/20" }} borderWidth="1px" borderColor="red.200">
                    <HStack gap="2" fontSize="xs" color="fg.error">
                      <Box as={AlertTriangle} boxSize="3" flexShrink="0" />
                      This action cannot be undone.
                    </HStack>
                  </Box>
                  <HStack justify="flex-end" gap="2.5" px="6" pt="4" pb="5">
                    <Button variant="ghost" onClick={handleCancel}>Cancel</Button>
                    <Button variant="danger" onClick={handleConfirm} disabled={confirmLocked}>{dialog.confirmLabel || "Delete"}</Button>
                  </HStack>
                </>
              )}

              {/* Non-destructive confirm / Alert */}
              {(dialog.type === "alert" || (dialog.type === "confirm" && !dialog.destructive)) && (
                <>
                  <Box px="6" pt="6" pb="2">
                    <HStack gap="3.5" align="start">
                      <Box w="10" h="10" borderRadius="xl" bg="blue.50" _dark={{ bg: "blue.900/30" }} display="flex" alignItems="center" justifyContent="center" flexShrink="0">
                        <Box as={Info} boxSize="4.5" color="blue.600" _dark={{ color: "blue.300" }} />
                      </Box>
                      <Box minW="0" pt="0.5">
                        <Text fontSize="md" fontWeight="semibold" color="fg" pr="8">{dialog.title}</Text>
                        {dialog.message && <Text fontSize="sm" color="fg.muted" mt="1.5">{dialog.message}</Text>}
                      </Box>
                    </HStack>
                  </Box>
                  <HStack justify="flex-end" gap="2.5" px="6" pt="4" pb="5">
                    {dialog.type !== "alert" && <Button variant="ghost" onClick={handleCancel}>Cancel</Button>}
                    <Button onClick={handleConfirm}>{dialog.type === "alert" ? "OK" : dialog.confirmLabel || "Confirm"}</Button>
                  </HStack>
                </>
              )}
            </Box>
          </Box>
        </Portal>
      )}
    </DialogContext.Provider>
  );
}
