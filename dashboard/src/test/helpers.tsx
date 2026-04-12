import { render, type RenderOptions } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ReactElement } from "react";

export function renderWithRouter(
  ui: ReactElement,
  { route = "/", ...options }: RenderOptions & { route?: string } = {}
) {
  return render(<MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>, options);
}
