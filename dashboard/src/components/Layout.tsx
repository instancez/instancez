import { Outlet } from "react-router-dom";
import { Navbar } from "./Navbar";
import { Sidebar } from "./Sidebar";
import { Button, SurfaceProvider } from "./ui";
import { DriftBanner } from "./DriftBanner";
import { EditModeBanner } from "./EditModeBanner";
import { useConfigStatus } from "../hooks/useConfigStatus";
import { useConfig } from "../hooks/useConfig";
import { ConsoleProvider } from "../console/ConsoleProvider";
import { adminBackend } from "../console/adminBackend";
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

/** Inner shell: reads config state from ConsoleProvider's context for the
 *  loading/error gate, then renders the full chrome + Outlet. */
function Shell() {
  const { loading, error, config, refresh } = useConfig();

  if (loading && !config) {
    return (
      <div className="min-h-dvh bg-background flex items-center justify-center">
        <div className="flex flex-col items-center gap-3">
          <Loader2 size={24} className="animate-spin text-muted-foreground" />
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
          <Button onClick={refresh}>
            <RefreshCw size={14} />
            Retry
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="h-dvh bg-background flex flex-col overflow-hidden">
      <Navbar />
      <div className="flex flex-1 min-h-0 gap-2 px-2 pb-2">
        <Sidebar />
        <main className="flex-1 min-w-0 overflow-y-auto bg-surface border border-border rounded-xl shadow-card">
          {/* Depth 1: page content sits on the surface card, so Panels
              inside it render as gray insets, and their children flip
              back to surface — every box contrasts with its parent. */}
          <SurfaceProvider depth={1}>
            <Outlet />
          </SurfaceProvider>
        </main>
      </div>
      {/* Banners anchor to the bottom of the shell as full-width strips,
          pushing the working area up rather than overlapping it. */}
      <StatusBanners />
    </div>
  );
}

/** Route element — wraps the entire console subtree in ConsoleProvider
 *  (BackendContext + ConfigContext + DialogProvider + SaveToast +
 *  ConfirmSaveDialog), then renders the loading/error gate and chrome. */
export function Layout() {
  return (
    <ConsoleProvider backend={adminBackend}>
      <Shell />
    </ConsoleProvider>
  );
}
