import { createContext, useContext, useState, useRef, useEffect, useCallback, useMemo } from "react";
import { X, AlertTriangle, Info, Trash2 } from "lucide-react";
import { Box, HStack, Input, Text, VStack } from "@chakra-ui/react";
import { Button, Field, Select } from "./ui";
import { Overlay } from "./Modal";

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
  validate?: (value: string) => string | null;
  owner?: symbol;
  resolve: (value: any) => void;
}

type PromptOptions = { message?: string; defaultValue?: string; placeholder?: string; validate?: (value: string) => string | null };

interface DialogContextValue {
  prompt: (title: string, options?: PromptOptions) => Promise<string | null>;
  confirm: (title: string, options?: { message?: string; confirmLabel?: string; destructive?: boolean; confirmText?: string }) => Promise<boolean>;
  alert: (title: string, options?: { message?: string }) => Promise<void>;
  select: (title: string, options: string[], extra?: { message?: string }) => Promise<string | null>;
}

interface ProviderValue extends DialogContextValue {
  open: (state: Omit<DialogState, "resolve" | "owner">, owner?: symbol) => Promise<any>;
  dismissOwned: (owner: symbol) => void;
}

function makeDialogApi(open: ProviderValue["open"], owner?: symbol): DialogContextValue {
  return {
    prompt: (title, o) => open({ type: "prompt", title, ...o }, owner),
    confirm: (title, o) => open({ type: "confirm", title, ...o, destructive: o?.destructive ?? true }, owner),
    alert: (title, o) => open({ type: "alert", title, message: o?.message }, owner),
    select: (title, options, extra) => open({ type: "select", title, options, message: extra?.message }, owner),
  };
}

const dismissed = (d: DialogState) => (d.type === "confirm" ? false : null);

const DialogContext = createContext<ProviderValue | null>(null);

/** Dialogs opened through this hook are cancelled when the calling page unmounts (tab or route change). */
export function useDialog(): DialogContextValue {
  const ctx = useContext(DialogContext);
  if (!ctx) throw new Error("useDialog must be used within DialogProvider");
  const owner = useRef(Symbol("dialog-owner")).current;
  useEffect(() => () => ctx.dismissOwned(owner), [ctx.dismissOwned, owner]);
  return useMemo(() => makeDialogApi(ctx.open, owner), [ctx.open, owner]);
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

  const current = useRef<DialogState | null>(null);
  current.current = dialog;

  const open = useCallback(
    (state: Omit<DialogState, "resolve" | "owner">, owner?: symbol) =>
      new Promise<any>((resolve) => {
        const prev = current.current;
        if (prev) prev.resolve(dismissed(prev));
        setInputValue(state.defaultValue || "");
        setConfirmInput("");
        setSelectValue(state.options?.[0] || "");
        setDialog({ ...state, owner, resolve });
      }),
    []
  );

  const dismissOwned = useCallback((owner: symbol) => {
    setDialog((cur) => {
      if (cur?.owner !== owner) return cur;
      cur.resolve(dismissed(cur));
      return null;
    });
    setVisible(false);
  }, []);

  const value = useMemo<ProviderValue>(
    () => ({ open, dismissOwned, ...makeDialogApi(open) }),
    [open, dismissOwned]
  );

  function close(result: string | boolean | null) {
    const closing = dialog;
    if (!closing) return;
    closing.resolve(result);
    setVisible(false);
    setTimeout(() => setDialog((cur) => (cur === closing ? null : cur)), 150);
  }

  const promptError = dialog?.type === "prompt" && dialog.validate && inputValue.trim()
    ? dialog.validate(inputValue.trim())
    : null;
  const promptValid = dialog?.type === "prompt" && !!inputValue.trim() && !promptError;
  const promptDirty = dialog?.type === "prompt" && inputValue !== (dialog.defaultValue || "");

  function handleConfirm() {
    if (confirmLocked) return;
    if (dialog?.type === "prompt") {
      if (!promptValid) return;
      close(inputValue.trim());
    } else if (dialog?.type === "select") {
      close(selectValue || null);
    } else if (dialog?.type === "confirm") {
      close(true);
    } else {
      close(null);
    }
  }

  function handleCancel() {
    if (dialog) close(dismissed(dialog));
  }

  const confirmLocked = dialog?.type === "confirm" && dialog.confirmText
    ? confirmInput !== dialog.confirmText
    : false;

  useEffect(() => {
    if (dialog) {
      requestAnimationFrame(() => setVisible(true));
      if (dialog.type === "prompt") {
        inputRef.current?.focus();
        inputRef.current?.select();
      } else if (dialog.type === "select") {
        selectRef.current?.focus();
      } else if (dialog.type === "confirm" && dialog.confirmText) {
        confirmInputRef.current?.focus();
      }
    }
  }, [dialog]);

  return (
    <DialogContext.Provider value={value}>
      {children}
      {dialog && (
        <Overlay onDismiss={handleCancel} dismissOnBackdrop={!promptDirty} visible={visible}>
            <Box
              role="dialog"
              aria-modal="true"
              aria-label={dialog.type === "prompt" ? undefined : dialog.title}
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
                aria-label="Close"
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
                          onKeyDown={(e) => { if (e.key === "Enter") handleConfirm(); }}
                          aria-invalid={!!promptError}
                          placeholder={dialog.placeholder || "Enter a name…"}
                          size="sm"
                        />
                      </Field>
                      {promptError ? (
                        <Text role="alert" fontSize="xs" color="fg.error">{promptError}</Text>
                      ) : (
                        dialog.message && <Text fontSize="xs" color="fg.muted">{dialog.message}</Text>
                      )}
                    </VStack>
                  </Box>
                  <HStack justify="flex-end" gap="2.5" px="6" pb="5">
                    <Button variant="ghost" onClick={handleCancel}>Cancel</Button>
                    <Button onClick={handleConfirm} disabled={!promptValid}>Create</Button>
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
                          onKeyDown={(e) => { if (e.key === "Enter") handleConfirm(); }}
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
        </Overlay>
      )}
    </DialogContext.Provider>
  );
}
