import { createContext, useContext, useState, useRef, useEffect, useCallback } from "react";
import { X, AlertTriangle, Info, Trash2 } from "lucide-react";

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
  const backdropRef = useRef<HTMLDivElement>(null);

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
        <div
          ref={backdropRef}
          onClick={(e) => {
            if (e.target === backdropRef.current) handleCancel();
          }}
          className={`fixed inset-0 z-50 flex items-center justify-center transition-all duration-200 ease-out ${
            visible ? "bg-black/50 backdrop-blur-[2px]" : "bg-black/0"
          }`}
        >
          <div
            className={`relative w-full max-w-[420px] mx-4 overflow-hidden rounded-2xl border shadow-2xl shadow-black/30 transition-all duration-200 ease-out ${
              dialog.type === "confirm" && dialog.destructive
                ? "border-destructive/30 bg-background"
                : "border-border bg-background"
            } ${
              visible
                ? "opacity-100 scale-100 translate-y-0"
                : "opacity-0 scale-95 translate-y-2"
            }`}
          >
            {/* Close button */}
            <button
              onClick={handleCancel}
              className="absolute top-4 right-4 p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer z-10"
            >
              <X size={14} />
            </button>

            {dialog.type === "prompt" ? (
              /* ── Prompt variant ── */
              <>
                <div className="px-6 pt-6 pb-5">
                  <div className="space-y-3">
                    <label htmlFor="dialog-input" className="block text-xs font-medium text-muted-foreground uppercase tracking-wider">
                      {dialog.title}
                    </label>
                    <input
                      ref={inputRef}
                      id="dialog-input"
                      type="text"
                      value={inputValue}
                      onChange={(e) => setInputValue(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && inputValue.trim()) handleConfirm();
                      }}
                      placeholder={dialog.placeholder || "Enter a name\u2026"}
                      className="w-full px-4 py-3 rounded-xl border border-border bg-surface text-[15px] text-foreground placeholder:text-muted-foreground/40 focus:outline-none focus:border-accent focus:ring-2 focus:ring-accent/20 transition-all font-medium"
                    />
                    {dialog.message && (
                      <p className="text-xs text-muted-foreground/70 leading-relaxed">{dialog.message}</p>
                    )}
                  </div>
                </div>
                <div className="flex items-center justify-end gap-2.5 px-6 pb-5">
                  <button
                    onClick={handleCancel}
                    className="px-4 py-2 rounded-lg text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={handleConfirm}
                    disabled={!inputValue.trim()}
                    className="px-5 py-2 rounded-lg text-sm font-medium bg-accent text-background hover:bg-accent-hover transition-all cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                  >
                    Create
                  </button>
                </div>
              </>

            ) : dialog.type === "select" ? (
              /* ── Select variant ── */
              <>
                <div className="px-6 pt-6 pb-5">
                  <div className="space-y-3">
                    <label htmlFor="dialog-select" className="block text-xs font-medium text-muted-foreground uppercase tracking-wider">
                      {dialog.title}
                    </label>
                    <select
                      ref={selectRef}
                      id="dialog-select"
                      value={selectValue}
                      onChange={(e) => setSelectValue(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && selectValue) handleConfirm();
                      }}
                      className="w-full px-4 py-3 rounded-xl border border-border bg-surface text-[15px] text-foreground focus:outline-none focus:border-accent focus:ring-2 focus:ring-accent/20 transition-all font-medium font-mono cursor-pointer appearance-none"
                    >
                      {(dialog.options || []).map((opt) => (
                        <option key={opt} value={opt}>{opt}</option>
                      ))}
                    </select>
                    {dialog.message && (
                      <p className="text-xs text-muted-foreground/70 leading-relaxed">{dialog.message}</p>
                    )}
                  </div>
                </div>
                <div className="flex items-center justify-end gap-2.5 px-6 pb-5">
                  <button
                    onClick={handleCancel}
                    className="px-4 py-2 rounded-lg text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={handleConfirm}
                    disabled={!selectValue}
                    className="px-5 py-2 rounded-lg text-sm font-medium bg-accent text-background hover:bg-accent-hover transition-all cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                  >
                    Select
                  </button>
                </div>
              </>

            ) : dialog.type === "confirm" && dialog.destructive ? (
              /* ── Destructive confirm variant ── */
              <>
                {/* Red top accent bar */}
                <div className="h-1 bg-destructive/60" />

                <div className="px-6 pt-5 pb-2">
                  <div className="flex items-start gap-3.5">
                    <div className="shrink-0 w-10 h-10 rounded-xl bg-destructive/15 flex items-center justify-center">
                      <Trash2 size={18} className="text-destructive" />
                    </div>
                    <div className="min-w-0 pt-0.5 flex-1">
                      <h2 className="text-[15px] font-semibold text-foreground leading-tight pr-8">{dialog.title}</h2>
                      {dialog.message && (
                        <p className="text-sm text-muted-foreground mt-1.5 leading-relaxed">{dialog.message}</p>
                      )}
                    </div>
                  </div>
                </div>

                {/* Type-to-confirm input */}
                {dialog.confirmText && (
                  <div className="px-6 pt-3 pb-1">
                    <label htmlFor="confirm-input" className="block text-xs text-muted-foreground mb-2 leading-relaxed">
                      Type <span className="font-mono font-semibold text-foreground">{dialog.confirmText}</span> to confirm
                    </label>
                    <input
                      ref={confirmInputRef}
                      id="confirm-input"
                      type="text"
                      value={confirmInput}
                      onChange={(e) => setConfirmInput(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && !confirmLocked) handleConfirm();
                      }}
                      placeholder={dialog.confirmText}
                      spellCheck={false}
                      autoComplete="off"
                      className="w-full px-4 py-2.5 rounded-xl border border-destructive/30 bg-surface text-sm font-mono text-foreground placeholder:text-muted-foreground/30 focus:outline-none focus:border-destructive focus:ring-2 focus:ring-destructive/20 transition-all"
                    />
                  </div>
                )}

                {/* Warning box */}
                <div className="mx-6 mt-4 px-3.5 py-2.5 rounded-lg bg-destructive/8 border border-destructive/15">
                  <p className="text-xs text-destructive leading-relaxed flex items-center gap-2">
                    <AlertTriangle size={12} className="shrink-0" />
                    This action cannot be undone.
                  </p>
                </div>

                <div className="flex items-center justify-end gap-2.5 px-6 pt-4 pb-5">
                  <button
                    onClick={handleCancel}
                    className="px-4 py-2 rounded-lg text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={handleConfirm}
                    disabled={confirmLocked}
                    className="px-5 py-2 rounded-lg text-sm font-medium bg-destructive text-white hover:bg-destructive-hover transition-all cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                  >
                    {dialog.confirmLabel || "Delete"}
                  </button>
                </div>
              </>

            ) : (
              /* ── Non-destructive confirm / Alert variant ── */
              <>
                <div className="px-6 pt-6 pb-2">
                  <div className="flex items-start gap-3.5">
                    <div className="shrink-0 w-10 h-10 rounded-xl bg-accent/10 flex items-center justify-center">
                      <Info size={18} className="text-accent" />
                    </div>
                    <div className="min-w-0 pt-0.5">
                      <h2 className="text-[15px] font-semibold text-foreground leading-tight pr-8">{dialog.title}</h2>
                      {dialog.message && (
                        <p className="text-sm text-muted-foreground mt-1.5 leading-relaxed">{dialog.message}</p>
                      )}
                    </div>
                  </div>
                </div>
                <div className="flex items-center justify-end gap-2.5 px-6 pt-4 pb-5">
                  {dialog.type !== "alert" && (
                    <button
                      onClick={handleCancel}
                      className="px-4 py-2 rounded-lg text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
                    >
                      Cancel
                    </button>
                  )}
                  <button
                    onClick={handleConfirm}
                    className="px-5 py-2 rounded-lg text-sm font-medium bg-accent text-background hover:bg-accent-hover transition-all cursor-pointer"
                  >
                    {dialog.type === "alert"
                      ? "OK"
                      : dialog.confirmLabel || "Confirm"}
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </DialogContext.Provider>
  );
}
