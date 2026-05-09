import { useEffect, useState } from "react";

type ToastState = {
  visible: boolean;
  source: string;
  statementCount: number;
};

let setStateFn: ((s: ToastState) => void) | null = null;

export function showSaveToast(opts: { source: string; statementCount: number }) {
  if (!setStateFn) return;
  setStateFn({ visible: true, source: opts.source, statementCount: opts.statementCount });
}

export function SaveToast() {
  const [state, setState] = useState<ToastState>({
    visible: false, source: "", statementCount: 0,
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
      className="fixed bottom-4 right-4 max-w-md rounded-md bg-emerald-50 border border-emerald-200 text-emerald-900 px-4 py-3 shadow-md text-sm z-50"
    >
      <div>
        Saved to <code>{state.source}</code>. Migrations applied:{" "}
        <strong>{state.statementCount} statement(s)</strong>.
      </div>
      <div className="mt-1 text-xs text-emerald-700">
        Reminder: update your git source to match, or your next external update will revert this.
      </div>
    </div>
  );
}
