import { createBrowserRouter, RouterProvider, Navigate } from "react-router-dom";
import { useState, useEffect, useMemo } from "react";
import { Layout } from "./components/Layout";
import { DialogProvider } from "./components/Dialog";
import { Login } from "./pages/Login";
import { consoleRoutes } from "./console/routes";

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

  // A data router is required for useMatches()/route handles, which the shell
  // uses to render page chrome. Built once the admin key is present.
  const router = useMemo(
    () =>
      createBrowserRouter(
        [
          {
            element: <Layout />,
            children: [
              ...consoleRoutes(),
              { path: "*", element: <Navigate to="/" replace /> },
            ],
          },
        ],
        { basename: "/dashboard" }
      ),
    []
  );

  if (!hasKey) {
    return (
      <DialogProvider>
        <Login onSuccess={() => setHasKey(true)} />
      </DialogProvider>
    );
  }

  return <RouterProvider router={router} />;
}
