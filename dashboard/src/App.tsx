import { Routes, Route, Navigate } from "react-router-dom";
import { useState, useEffect, lazy, Suspense } from "react";
import { Loader2 } from "lucide-react";
import { Layout } from "./components/Layout";
import { DialogProvider } from "./components/Dialog";
import { DriftBanner } from "./components/DriftBanner";
import { EditModeBanner } from "./components/EditModeBanner";
import { SaveToast } from "./components/SaveToast";
import { useConfigStatus } from "./hooks/useConfigStatus";
import { Login } from "./pages/Login";

const Overview = lazy(() => import("./pages/Overview").then((m) => ({ default: m.Overview })));
const Tables = lazy(() => import("./pages/Tables").then((m) => ({ default: m.Tables })));
const TableDetail = lazy(() => import("./pages/TableDetail").then((m) => ({ default: m.TableDetail })));
const AuthPage = lazy(() => import("./pages/Auth").then((m) => ({ default: m.AuthPage })));
const Storage = lazy(() => import("./pages/Storage").then((m) => ({ default: m.Storage })));
const StorageDetail = lazy(() => import("./pages/StorageDetail").then((m) => ({ default: m.StorageDetail })));
const Functions = lazy(() => import("./pages/Functions").then((m) => ({ default: m.Functions })));
const FunctionDetail = lazy(() => import("./pages/FunctionDetail").then((m) => ({ default: m.FunctionDetail })));
const Events = lazy(() => import("./pages/Events").then((m) => ({ default: m.Events })));
const EventDetail = lazy(() => import("./pages/EventDetail").then((m) => ({ default: m.EventDetail })));
const ProvidersPage = lazy(() => import("./pages/Providers").then((m) => ({ default: m.ProvidersPage })));
const SettingsPage = lazy(() => import("./pages/Settings").then((m) => ({ default: m.SettingsPage })));

function PageLoader() {
  return (
    <div className="flex items-center justify-center py-24">
      <Loader2 size={20} className="animate-spin text-accent" />
    </div>
  );
}

function StatusBanners() {
  const { data } = useConfigStatus();
  return (
    <>
      <DriftBanner status={data} />
      <EditModeBanner status={data} />
    </>
  );
}

export function App() {
  const [hasKey, setHasKey] = useState(
    () => !!sessionStorage.getItem("ultrabase_admin_key")
  );

  useEffect(() => {
    const handler = () => {
      setHasKey(!!sessionStorage.getItem("ultrabase_admin_key"));
    };
    window.addEventListener("storage", handler);
    return () => window.removeEventListener("storage", handler);
  }, []);

  if (!hasKey) {
    return (
      <DialogProvider>
        <Login onSuccess={() => setHasKey(true)} />
      </DialogProvider>
    );
  }

  return (
    <DialogProvider>
      <StatusBanners />
      <SaveToast />
      <Suspense fallback={<PageLoader />}>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<Overview />} />
            <Route path="tables" element={<Tables />} />
            <Route path="tables/:name" element={<TableDetail />} />
            <Route path="auth" element={<AuthPage />} />
            <Route path="storage" element={<Storage />} />
            <Route path="storage/:name" element={<StorageDetail />} />
            <Route path="functions" element={<Functions />} />
            <Route path="functions/:name" element={<FunctionDetail />} />
            <Route path="events" element={<Events />} />
            <Route path="events/:name" element={<EventDetail />} />
            <Route path="providers" element={<ProvidersPage />} />
            <Route path="settings" element={<SettingsPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </Suspense>
    </DialogProvider>
  );
}
