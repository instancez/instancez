import { Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { DriftBanner } from "./DriftBanner";
import { EditModeBanner } from "./EditModeBanner";
import { useConfigStatus } from "../hooks/useConfigStatus";
import { ConfigContext, useConfigState } from "../hooks/useConfig";
import { Loader2, AlertCircle, RefreshCw } from "lucide-react";

function StatusBanners() {
  const { data } = useConfigStatus();
  return (
    <>
      <DriftBanner status={data} />
      <EditModeBanner status={data} />
    </>
  );
}

export function Layout() {
  const configState = useConfigState();
  const { loading, error, config, refresh } = configState;

  if (loading && !config) {
    return (
      <div className="min-h-dvh bg-background flex items-center justify-center">
        <div className="flex flex-col items-center gap-3">
          <Loader2 size={24} className="animate-spin text-accent" />
          <p className="text-sm text-muted-foreground">Loading configuration...</p>
        </div>
      </div>
    );
  }

  if (error && !config) {
    return (
      <div className="min-h-dvh bg-background flex items-center justify-center">
        <div className="flex flex-col items-center gap-4 max-w-sm text-center">
          <AlertCircle size={32} className="text-destructive" />
          <p className="text-sm text-muted-foreground">{error}</p>
          <button
            onClick={refresh}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-surface text-foreground text-sm font-medium hover:bg-surface-hover transition-colors cursor-pointer"
          >
            <RefreshCw size={14} />
            Retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <ConfigContext.Provider value={configState}>
      <div className="h-dvh bg-background flex flex-col overflow-hidden">
        {/* Banners sit at the top of the shell so they push the sidebar AND
            content down, rather than being overlapped by a fixed sidebar. */}
        <StatusBanners />
        <div className="flex flex-1 min-h-0">
          <Sidebar />
          <main className="flex-1 min-w-0 overflow-y-auto">
            <Outlet />
          </main>
        </div>
      </div>
    </ConfigContext.Provider>
  );
}
