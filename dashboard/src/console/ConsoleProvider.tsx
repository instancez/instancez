import { type ReactNode } from "react";
import { BackendProvider } from "./BackendContext";
import type { ConsoleBackend } from "./backend";
import { ConfigContext, useConfigState } from "../hooks/useConfig";
import { DialogProvider } from "../components/Dialog";
import { ConfirmSaveDialog } from "../components/ConfirmSaveDialog";
import { SaveToast } from "../components/SaveToast";

function ConfigShell({ children }: { children: ReactNode }) {
  const configState = useConfigState();
  return (
    <ConfigContext.Provider value={configState}>
      <DialogProvider>
        {children}
        <SaveToast />
        {configState.pendingSave && (
          <ConfirmSaveDialog
            current={configState.pendingSave.current}
            proposed={configState.pendingSave.proposed}
            dotenvChanges={configState.pendingSave.dotenvChanges}
            saving={configState.saving}
            onConfirm={configState.confirmPendingSave}
            onCancel={configState.cancelPendingSave}
          />
        )}
      </DialogProvider>
    </ConfigContext.Provider>
  );
}

export function ConsoleProvider({
  backend,
  children,
}: {
  backend: ConsoleBackend;
  children: ReactNode;
}) {
  return (
    <BackendProvider backend={backend}>
      <ConfigShell>{children}</ConfigShell>
    </BackendProvider>
  );
}
