import { Routes, Route } from "react-router-dom";
import { Layout } from "./components/Layout";
import { DocPageView } from "./components/DocPageView";
import { pages } from "./content";

export function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        {pages.map((page) =>
          page.slug === "" ? (
            <Route
              key="index"
              index
              element={<DocPageView page={page} />}
            />
          ) : (
            <Route
              key={page.slug}
              path={page.slug}
              element={<DocPageView page={page} />}
            />
          )
        )}
      </Route>
    </Routes>
  );
}
