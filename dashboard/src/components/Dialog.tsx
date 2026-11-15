import { createContext, useContext, useState, useRef, useEffect, useCallback, useMemo } from "react";
import { Box, Input, Text } from "@chakra-ui/react";
import { Field, Select } from "./ui";
import { DialogFrame } from "./DialogFrame";

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

function primaryLabel(d: DialogState) {
  if (d.type === "prompt") return "Create";
  if (d.type === "select") return "Select";
  if (d.type === "alert") return "OK";
  return d.confirmLabel || (d.destructive ? "Delete" : "Confirm");
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
        const next = { ...state, owner, resolve };
        current.current = next;
        setDialog(next);
      }),
    []
  );

  const dismissOwned = useCallback((owner: symbol) => {
    const cur = current.current;
    if (cur?.owner !== owner) return;
    cur.resolve(dismissed(cur));
    current.current = null;
    setDialog(null);
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

  const confirmLocked = dialog?.type === "confirm" && dialog.destructive && dialog.confirmText
    ? confirmInput !== dialog.confirmText
    : false;

  const primaryDisabled =
    dialog?.type === "prompt" ? !promptValid : dialog?.type === "select" ? !selectValue : confirmLocked;

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
        <DialogFrame
          title={dialog.title}
          description={dialog.message}
          tone={dialog.type === "confirm" && dialog.destructive ? "danger" : "default"}
          error={promptError}
          dirty={promptDirty}
          visible={visible}
          onClose={handleCancel}
          secondary={dialog.type === "alert" ? false : undefined}
          primary={{ label: primaryLabel(dialog), onClick: handleConfirm, disabled: primaryDisabled }}
        >
          {dialog.type === "prompt" && (
            <Input
              ref={inputRef}
              type="text"
              aria-label={dialog.title}
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter") handleConfirm(); }}
              aria-invalid={!!promptError}
              placeholder={dialog.placeholder || "Enter a name…"}
              size="sm"
            />
          )}
          {dialog.type === "select" && (
            <Select
              aria-label={dialog.title}
              value={selectValue}
              onChange={(e) => setSelectValue((e.target as HTMLSelectElement).value)}
              mono
            >
              {(dialog.options || []).map((opt) => (
                <option key={opt} value={opt}>{opt}</option>
              ))}
            </Select>
          )}
          {dialog.type === "confirm" && dialog.destructive && (
            <>
              {dialog.confirmText && (
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
              )}
              <Text fontSize="sm" color="fg.muted">This action cannot be undone.</Text>
            </>
          )}
        </DialogFrame>
      )}
    </DialogContext.Provider>
  );
}
