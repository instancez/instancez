import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ChakraProvider, createSystem, defaultConfig } from "@chakra-ui/react";
import { ColorModeProvider } from "./components/color-mode";
import { App } from "./App";
import "./index.css";

const consoleSystem = createSystem(defaultConfig, {
  theme: {
    tokens: {
      fonts: {
        heading: { value: "'Bricolage Grotesque', ui-sans-serif, sans-serif" },
        body: { value: "'Bricolage Grotesque', ui-sans-serif, sans-serif" },
        mono: { value: "'JetBrains Mono', ui-monospace, monospace" },
      },
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ChakraProvider value={consoleSystem}>
      <ColorModeProvider>
        <App />
      </ColorModeProvider>
    </ChakraProvider>
  </StrictMode>
);
