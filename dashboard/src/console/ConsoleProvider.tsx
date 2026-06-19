import { type ReactNode, Suspense } from "react";
import { BackendProvider } from "./BackendContext";
import type { ConsoleBackend } from "./backend";
import type { Config } from "../lib/types";
import { ConfigContext, useConfigState } from "../hooks/useConfig";
import { DialogProvider } from "../components/Dialog";
import { ConfirmSaveDialog } from "../components/ConfirmSaveDialog";
import { SaveToast } from "../components/SaveToast";

function ConfigShell({ initialConfig, children }: { initialConfig?: Config | null; children: ReactNode }) {
  const configState = useConfigState(initialConfig);
  return (
    <ConfigContext.Provider value={configState}>
      <DialogProvider>
        <Suspense fallback={null}>
          {children}
        </Suspense>
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
  initialConfig,
  children,
}: {
  backend: ConsoleBackend;
  initialConfig?: Config | null;
  children: ReactNode;
}) {
  return (
    <BackendProvider backend={backend}>
      <ConfigShell initialConfig={initialConfig}>{children}</ConfigShell>
    </BackendProvider>
  );
}
