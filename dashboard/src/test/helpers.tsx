import { render, type RenderOptions } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { createSystem, defaultConfig, ChakraProvider } from "@chakra-ui/react";
import { ColorModeProvider } from "../components/color-mode";
import type { ReactElement } from "react";

const system = createSystem(defaultConfig);

export function renderWithChakra(ui: ReactElement) {
  return render(
    <ChakraProvider value={system}>
      <ColorModeProvider>{ui}</ColorModeProvider>
    </ChakraProvider>
  );
}

export function renderWithRouter(
  ui: ReactElement,
  { route = "/", ...options }: RenderOptions & { route?: string } = {}
) {
  return render(<MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>, options);
}
