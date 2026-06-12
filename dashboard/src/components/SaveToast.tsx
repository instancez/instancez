import { useEffect, useState } from "react";

type ToastState = {
  visible: boolean;
  source: string;
};

let setStateFn: ((s: ToastState) => void) | null = null;

export function showSaveToast(opts: { source: string }) {
  if (!setStateFn) return;
  setStateFn({ visible: true, source: opts.source });
}

export function SaveToast() {
  const [state, setState] = useState<ToastState>({
    visible: false, source: "",
  });

  useEffect(() => {
    setStateFn = setState;
    return () => { setStateFn = null; };
  }, []);

  useEffect(() => {
    if (!state.visible) return;
    const t = setTimeout(() => setState((s) => ({ ...s, visible: false })), 8000);
    return () => clearTimeout(t);
  }, [state.visible]);

  if (!state.visible) return null;

  return (
    <div
      role="status"
      className="fixed bottom-6 right-6 max-w-md rounded-xl border border-border bg-surface text-foreground shadow-lifted px-4 py-3 text-sm z-50 animate-rise"
    >
      <div>
        Saved to <code className="font-mono">{state.source}</code>.
      </div>
      <div className="mt-1 text-xs text-muted-foreground">
        Reminder: update your git source to match, or your next external update will revert this.
      </div>
    </div>
  );
}
