import { render, screen } from "@testing-library/react";
import { ConsoleProvider } from "./ConsoleProvider";
import { useApiBaseUrl } from "./BackendContext";

const stubBackend = { capabilities: {}, getConfig: async () => ({}) } as any;
function Probe() { return <span>{useApiBaseUrl()}</span>; }

test("useApiBaseUrl returns the provided apiBaseUrl", () => {
  render(
    <ConsoleProvider backend={stubBackend} apiBaseUrl="https://beacon-7x2.instancez.app/api">
      <Probe />
    </ConsoleProvider>
  );
  expect(screen.getByText("https://beacon-7x2.instancez.app/api")).toBeInTheDocument();
});

test("useApiBaseUrl defaults to window.location.origin", () => {
  render(<ConsoleProvider backend={stubBackend}><Probe /></ConsoleProvider>);
  expect(screen.getByText(window.location.origin)).toBeInTheDocument();
});
