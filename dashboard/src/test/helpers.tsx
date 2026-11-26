import { render, type RenderOptions } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { createSystem, defaultConfig, ChakraProvider } from "@chakra-ui/react";
import { ColorModeProvider } from "../components/color-mode";
import type { ReactElement, ReactNode } from "react";

const system = createSystem(defaultConfig);

// A wrapper (not inline JSX) so rerender() keeps the providers.
function Providers({ children }: { children: ReactNode }) {
  return (
    <ChakraProvider value={system}>
      <ColorModeProvider>{children}</ColorModeProvider>
    </ChakraProvider>
  );
}

export function renderWithChakra(ui: ReactElement) {
  return render(ui, { wrapper: Providers });
}

export function renderWithRouter(
  ui: ReactElement,
  { route = "/", ...options }: RenderOptions & { route?: string } = {}
) {
  return render(<MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>, options);
}
