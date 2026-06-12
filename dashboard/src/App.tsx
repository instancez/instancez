import { useRoutes, Navigate } from "react-router-dom";
import { useState, useEffect, Suspense } from "react";
import { Loader2 } from "lucide-react";
import { Layout } from "./components/Layout";
import { DialogProvider } from "./components/Dialog";
import { Login } from "./pages/Login";
import { consoleRoutes } from "./console/routes";

function PageLoader() {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-24">
      <Loader2 size={20} className="animate-spin text-muted-foreground" />
      <p className="t-label">Loading</p>
    </div>
  );
}

function AppRoutes() {
  return (
    <Suspense fallback={<PageLoader />}>
      {useRoutes([
        {
          element: <Layout />,
          children: [
            ...consoleRoutes(),
            { path: "*", element: <Navigate to="/" replace /> },
          ],
        },
      ])}
    </Suspense>
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

  return <AppRoutes />;
}
