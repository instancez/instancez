import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { ApiKeys } from "./ApiKeys";

vi.mock("../api/client", () => ({
  getKeys: vi.fn(),
}));

import { getKeys } from "../api/client";

const mockGetKeys = vi.mocked(getKeys);

const ANON_KEY = "eyJhbGciOiJSUzI1NiJ9.anon.signature";
const ADMIN_KEY = "super-secret-admin-key";

describe("ApiKeys", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.setItem("instancez_admin_key", ADMIN_KEY);
    mockGetKeys.mockResolvedValue({ anon_key: ANON_KEY });
  });

  it("shows the API URL", () => {
    renderWithChakra(<ApiKeys />);
    expect(screen.getByText(window.location.origin)).toBeInTheDocument();
  });

  it("shows the anon key in the clear once loaded", async () => {
    renderWithChakra(<ApiKeys />);
    await waitFor(() => {
      expect(screen.getByText(ANON_KEY)).toBeInTheDocument();
    });
  });

  it("masks the admin key until revealed", async () => {
    renderWithChakra(<ApiKeys />);
    expect(screen.queryByText(ADMIN_KEY)).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("Reveal admin"));
    expect(screen.getByText(ADMIN_KEY)).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("Hide admin"));
    expect(screen.queryByText(ADMIN_KEY)).not.toBeInTheDocument();
  });

  it("copies the anon key to the clipboard", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    renderWithChakra(<ApiKeys />);
    await waitFor(() => {
      expect(screen.getByText(ANON_KEY)).toBeInTheDocument();
    });

    fireEvent.click(screen.getByLabelText("Copy anon"));
    expect(writeText).toHaveBeenCalledWith(ANON_KEY);
  });

  it("hides the anon row when the keys endpoint fails", async () => {
    mockGetKeys.mockRejectedValue(new Error("404"));
    renderWithChakra(<ApiKeys />);
    await waitFor(() => {
      expect(mockGetKeys).toHaveBeenCalled();
    });
    expect(screen.queryByText("anon")).not.toBeInTheDocument();
  });

  it("is compact: no per-key description paragraphs", async () => {
    renderWithChakra(<ApiKeys />);
    await waitFor(() => expect(screen.getByText(ANON_KEY)).toBeInTheDocument());
    expect(screen.queryByText(/Pass as the first argument/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Safe to use in a browser/)).not.toBeInTheDocument();
    expect(screen.queryByText(/never ship it to a browser/)).not.toBeInTheDocument();
  });

  it("hides the admin row when no admin key is stored", () => {
    sessionStorage.removeItem("instancez_admin_key");
    renderWithChakra(<ApiKeys />);
    expect(screen.queryByText("admin")).not.toBeInTheDocument();
  });
});
