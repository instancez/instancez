import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { BackendProvider } from "../console/BackendContext";
import { DialogProvider } from "../components/Dialog";
import { UsersPage } from "./Users";
import type { ConsoleBackend } from "../console/backend";
import type { AdminUser } from "../lib/types";

const alice: AdminUser = {
  id: "u1",
  email: "alice@example.com",
  email_confirmed_at: "2026-01-01T00:00:00Z",
  banned_until: "",
  last_sign_in_at: "2026-06-01T00:00:00Z",
  app_metadata: {},
  user_metadata: {},
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

const bannedUser: AdminUser = {
  ...alice,
  id: "u2",
  email: "banned@example.com",
  banned_until: "2099-01-01T00:00:00Z",
};

function makeBackend(overrides: Partial<ConsoleBackend> = {}): ConsoleBackend {
  return {
    capabilities: {
      canWriteConfig: false,
      canWriteSecrets: false,
      canEditFunctionCode: false,
      canManageDeps: false,
      hasStats: false,
    },
    getConfig: vi.fn(),
    getConfigStatus: vi.fn(),
    previewConfig: vi.fn(),
    putConfig: vi.fn(),
    getEnvVars: vi.fn(),
    writeSecrets: vi.fn(),
    getKeys: vi.fn(),
    getStats: vi.fn(),
    getConfigDiff: vi.fn(),
    getFunctionCode: vi.fn(),
    putFunctionCode: vi.fn(),
    checkFunctionFile: vi.fn(),
    getFunctionDeps: vi.fn(),
    postFunctionDeps: vi.fn(),
    listUsers: vi.fn().mockResolvedValue({ users: [], total: 0 }),
    createUser: vi.fn().mockResolvedValue(alice),
    updateUser: vi.fn().mockResolvedValue(alice),
    deleteUser: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  } as unknown as ConsoleBackend;
}

function renderUsersPage(backend: ConsoleBackend) {
  return renderWithChakra(
    <BackendProvider backend={backend}>
      <MemoryRouter>
        <DialogProvider>
          <UsersPage />
        </DialogProvider>
      </MemoryRouter>
    </BackendProvider>
  );
}

describe("UsersPage", () => {
  it("shows empty state when no users", async () => {
    renderUsersPage(makeBackend());
    expect(await screen.findByText("No users yet")).toBeInTheDocument();
  });

  it("renders user rows", async () => {
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
    });
    renderUsersPage(backend);
    expect(await screen.findByText("alice@example.com")).toBeInTheDocument();
  });

  it("shows Banned badge for banned users", async () => {
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [bannedUser], total: 1 }),
    });
    renderUsersPage(backend);
    expect(await screen.findByText("Banned")).toBeInTheDocument();
  });

  it("opens create modal when Add user clicked", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");
    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    expect(screen.getByText("Create user")).toBeInTheDocument();
  });

  it("opens edit modal when user row clicked", async () => {
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
    });
    renderUsersPage(backend);
    fireEvent.click(await screen.findByText("alice@example.com"));
    expect(screen.getByText("Edit user")).toBeInTheDocument();
  });

  it("calls createUser on form submit in create mode", async () => {
    const createUser = vi.fn().mockResolvedValue(alice);
    const backend = makeBackend({ createUser });
    renderUsersPage(backend);
    await screen.findByText("No users yet");

    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: "alice@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: "secret123" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create user/i }));

    await waitFor(() => expect(createUser).toHaveBeenCalledWith("alice@example.com", "secret123", false));
  });

  it("calls updateUser on save in edit mode", async () => {
    const updateUser = vi.fn().mockResolvedValue(alice);
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
      updateUser,
    });
    renderUsersPage(backend);

    fireEvent.click(await screen.findByText("alice@example.com"));
    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: "new@example.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() =>
      expect(updateUser).toHaveBeenCalledWith("u1", expect.objectContaining({ email: "new@example.com" }))
    );
  });

  it("calls deleteUser after confirm in edit mode", async () => {
    const deleteUser = vi.fn().mockResolvedValue(undefined);
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
      deleteUser,
    });
    renderUsersPage(backend);

    fireEvent.click(await screen.findByText("alice@example.com"));
    fireEvent.click(screen.getByRole("button", { name: /^delete$/i }));

    // The confirm dialog appears with a text input gated by the user's email.
    // Type the email to unlock the "Delete user" confirm button.
    const input = await screen.findByPlaceholderText("alice@example.com");
    fireEvent.change(input, { target: { value: "alice@example.com" } });
    fireEvent.click(await screen.findByRole("button", { name: /delete user/i }));

    await waitFor(() => expect(deleteUser).toHaveBeenCalledWith("u1"));
  });

  it("shows error when createUser fails", async () => {
    const createUser = vi.fn().mockRejectedValue(
      Object.assign(new Error("Email already exists"), { body: { message: "Email already exists" } })
    );
    const backend = makeBackend({ createUser });
    renderUsersPage(backend);
    await screen.findByText("No users yet");

    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: "dupe@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: "secret123" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create user/i }));

    expect(await screen.findByText("Email already exists")).toBeInTheDocument();
  });

  it("shows validation error when email is empty on create", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");

    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.click(screen.getByRole("button", { name: /create user/i }));

    expect(screen.getByText("Email is required.")).toBeInTheDocument();
  });

  it("shows validation error for invalid email format on create", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");

    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.change(screen.getByLabelText(/email/i), { target: { value: "notanemail" } });
    fireEvent.click(screen.getByRole("button", { name: /create user/i }));

    expect(screen.getByText("Enter a valid email address.")).toBeInTheDocument();
  });

  it("shows validation error when password is too short on create", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");

    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.change(screen.getByLabelText(/email/i), { target: { value: "alice@example.com" } });
    fireEvent.change(screen.getByLabelText(/password/i), { target: { value: "abc" } });
    fireEvent.click(screen.getByRole("button", { name: /create user/i }));

    expect(screen.getByText("Password must be at least 6 characters.")).toBeInTheDocument();
  });

  it("shows validation error for invalid email format on edit", async () => {
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
    });
    renderUsersPage(backend);

    fireEvent.click(await screen.findByText("alice@example.com"));
    fireEvent.change(screen.getByLabelText(/email/i), { target: { value: "bademail" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    expect(screen.getByText("Enter a valid email address.")).toBeInTheDocument();
  });

  it("shows validation error when password is too short on edit", async () => {
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
    });
    renderUsersPage(backend);

    fireEvent.click(await screen.findByText("alice@example.com"));
    fireEvent.change(screen.getByLabelText(/password/i), { target: { value: "ab" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    expect(screen.getByText("Password must be at least 6 characters.")).toBeInTheDocument();
  });
});
