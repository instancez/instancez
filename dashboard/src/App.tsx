import { Routes, Route, Navigate } from "react-router-dom";
import { useState, useEffect, lazy, Suspense } from "react";
import { Loader2 } from "lucide-react";
import { Layout } from "./components/Layout";
import { DialogProvider } from "./components/Dialog";
import { SaveToast } from "./components/SaveToast";
import { Login } from "./pages/Login";

const Overview = lazy(() => import("./pages/Overview").then((m) => ({ default: m.Overview })));
const Tables = lazy(() => import("./pages/Tables").then((m) => ({ default: m.Tables })));
const TableDetail = lazy(() => import("./pages/TableDetail").then((m) => ({ default: m.TableDetail })));
const AuthPage = lazy(() => import("./pages/Auth").then((m) => ({ default: m.AuthPage })));
const Storage = lazy(() => import("./pages/Storage").then((m) => ({ default: m.Storage })));
const StorageDetail = lazy(() => import("./pages/StorageDetail").then((m) => ({ default: m.StorageDetail })));
const Rpc = lazy(() => import("./pages/Rpc").then((m) => ({ default: m.Rpc })));
const RpcDetail = lazy(() => import("./pages/RpcDetail").then((m) => ({ default: m.RpcDetail })));
const Functions = lazy(() => import("./pages/Functions").then((m) => ({ default: m.Functions })));
const FunctionDetail = lazy(() => import("./pages/FunctionDetail").then((m) => ({ default: m.FunctionDetail })));
const ProvidersPage = lazy(() => import("./pages/Providers").then((m) => ({ default: m.ProvidersPage })));

function PageLoader() {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-24">
      <Loader2 size={20} className="animate-spin text-muted-foreground" />
      <p className="t-label">Loading</p>
    </div>
  );
}

export function App() {
  const [hasKey, setHasKey] = useState(
    () => !!sessionStorage.getItem("instancez_admin_key")
  );

  useEffect(() => {
    const handler = () => {
      setHasKey(!!sessionStorage.getItem("instancez_admin_key"));
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
            <Route path="rpc" element={<Rpc />} />
            <Route path="rpc/:name" element={<RpcDetail />} />
            <Route path="functions" element={<Functions />} />
            <Route path="functions/:name" element={<FunctionDetail />} />
            <Route path="providers" element={<ProvidersPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </Suspense>
    </DialogProvider>
  );
}
