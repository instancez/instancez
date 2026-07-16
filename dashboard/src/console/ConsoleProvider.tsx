import { type ReactNode, Suspense } from "react";
import { BackendProvider, ApiBaseUrlProvider } from "./BackendContext";
import type { ConsoleBackend } from "./backend";
import type { Config } from "../lib/types";
import { ConfigContext, useConfigState } from "../hooks/useConfig";
import { DialogProvider } from "../components/Dialog";
import { ConfirmSaveDialog } from "../components/ConfirmSaveDialog";
import { SaveToast } from "../components/SaveToast";
import { ListSkeleton } from "../components/Skeletons";

function ConfigShell({ initialConfig, children }: { initialConfig?: Config | null; children: ReactNode }) {
  const configState = useConfigState(initialConfig);
  return (
    <ConfigContext.Provider value={configState}>
      <DialogProvider>
        <Suspense fallback={<ListSkeleton rows={6} />}>
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
  apiBaseUrl = window.location.origin,
  children,
}: {
  backend: ConsoleBackend;
  initialConfig?: Config | null;
  apiBaseUrl?: string;
  children: ReactNode;
}) {
  return (
    <ApiBaseUrlProvider value={apiBaseUrl}>
      <BackendProvider backend={backend}>
        <ConfigShell initialConfig={initialConfig}>{children}</ConfigShell>
      </BackendProvider>
    </ApiBaseUrlProvider>
  );
}
