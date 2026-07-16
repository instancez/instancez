import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { ApiKeys } from "./ApiKeys";

vi.mock("../api/client", () => ({
  getKeys: vi.fn(),
}));

import { getKeys } from "../api/client";

const mockGetKeys = vi.mocked(getKeys);

const PUBLISHABLE_KEY = "inz_publishable_testpub123";
const SECRET_KEY = "inz_secret_testsecret123";

describe("ApiKeys", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.setItem("instancez_secret_key", SECRET_KEY);
    mockGetKeys.mockResolvedValue({ publishable_key: PUBLISHABLE_KEY });
  });

  it("shows the API URL", () => {
    renderWithChakra(<ApiKeys />);
    expect(screen.getByText(window.location.origin)).toBeInTheDocument();
  });

  it("shows the publishable key in the clear once loaded", async () => {
    renderWithChakra(<ApiKeys />);
    await waitFor(() => {
      expect(screen.getByText(PUBLISHABLE_KEY)).toBeInTheDocument();
    });
  });

  it("masks the secret key until revealed", async () => {
    renderWithChakra(<ApiKeys />);
    expect(screen.queryByText(SECRET_KEY)).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("Reveal secret"));
    expect(screen.getByText(SECRET_KEY)).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("Hide secret"));
    expect(screen.queryByText(SECRET_KEY)).not.toBeInTheDocument();
  });

  it("copies the publishable key to the clipboard", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    renderWithChakra(<ApiKeys />);
    await waitFor(() => {
      expect(screen.getByText(PUBLISHABLE_KEY)).toBeInTheDocument();
    });

    fireEvent.click(screen.getByLabelText("Copy publishable"));
    expect(writeText).toHaveBeenCalledWith(PUBLISHABLE_KEY);
  });

  it("hides the publishable row when the keys endpoint fails", async () => {
    mockGetKeys.mockRejectedValue(new Error("404"));
    renderWithChakra(<ApiKeys />);
    await waitFor(() => {
      expect(mockGetKeys).toHaveBeenCalled();
    });
    expect(screen.queryByText("publishable")).not.toBeInTheDocument();
  });

  it("is compact: no per-key description paragraphs", async () => {
    renderWithChakra(<ApiKeys />);
    await waitFor(() => expect(screen.getByText(PUBLISHABLE_KEY)).toBeInTheDocument());
    expect(screen.queryByText(/Pass as the first argument/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Safe to use in a browser/)).not.toBeInTheDocument();
    expect(screen.queryByText(/never ship it to a browser/)).not.toBeInTheDocument();
  });

  it("hides the secret row when no secret key is stored", () => {
    sessionStorage.removeItem("instancez_secret_key");
    renderWithChakra(<ApiKeys />);
    expect(screen.queryByText("secret")).not.toBeInTheDocument();
  });

  it("polls the empty key while the backend is being created, then stops once it appears", async () => {
    vi.useFakeTimers();
    try {
      // Empty while the instance is still coming up, then the key arrives.
      mockGetKeys
        .mockResolvedValueOnce({ publishable_key: "" })
        .mockResolvedValueOnce({ publishable_key: "" })
        .mockResolvedValue({ publishable_key: PUBLISHABLE_KEY });

      renderWithChakra(<ApiKeys />);

      // First fetch resolves empty: the creating hint shows, no key yet.
      await vi.advanceTimersByTimeAsync(0);
      expect(screen.queryByText(PUBLISHABLE_KEY)).not.toBeInTheDocument();
      expect(screen.getByText(/creating/i)).toBeInTheDocument();

      // Two poll intervals later the third response carries the key.
      await vi.advanceTimersByTimeAsync(8000);
      expect(screen.getByText(PUBLISHABLE_KEY)).toBeInTheDocument();

      // Having found it, the hook stops polling: no further calls.
      const calls = mockGetKeys.mock.calls.length;
      await vi.advanceTimersByTimeAsync(8000);
      expect(mockGetKeys).toHaveBeenCalledTimes(calls);
    } finally {
      vi.useRealTimers();
    }
  });
});
