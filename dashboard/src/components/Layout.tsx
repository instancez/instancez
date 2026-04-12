import { Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { ConfigContext, useConfigState } from "../hooks/useConfig";
import { Loader2, AlertCircle, RefreshCw } from "lucide-react";

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
      <div className="min-h-dvh bg-background">
        <Sidebar />
        <main className="ml-[272px] min-h-dvh">
          <Outlet />
        </main>
      </div>
    </ConfigContext.Provider>
  );
}
