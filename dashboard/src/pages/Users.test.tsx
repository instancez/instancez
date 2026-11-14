import userEvent from "@testing-library/user-event";
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
      canEditFunctionFile: false,
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
    expect(screen.getByText("Add user", { selector: "p" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create" })).toBeInTheDocument();
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
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

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
    fireEvent.click(screen.getByRole("button", { name: /^delete user$/i }));

    expect(await screen.findByText("Delete user?")).toBeInTheDocument();
    const confirm = await screen.findByRole("button", { name: /^delete$/i });
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox", { name: /type alice@example.com/i }), "alice@example.co");
    expect(confirm).toBeDisabled();
    expect(deleteUser).not.toHaveBeenCalled();
    await userEvent.type(screen.getByRole("textbox", { name: /type alice@example.com/i }), "m");
    fireEvent.click(confirm);

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
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    expect(await screen.findByText("Email already exists")).toBeInTheDocument();
  });

  it("shows validation error when email is empty on create", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");

    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    expect(screen.getByText("Email is required.")).toBeInTheDocument();
  });

  it("shows validation error for invalid email format on create", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");

    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.change(screen.getByLabelText(/email/i), { target: { value: "notanemail" } });
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    expect(screen.getByText("Enter a valid email address.")).toBeInTheDocument();
  });

  it("shows validation error when password is too short on create", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");

    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.change(screen.getByLabelText(/email/i), { target: { value: "alice@example.com" } });
    fireEvent.change(screen.getByLabelText(/password/i), { target: { value: "abc" } });
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

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

  it("shows a skeleton while users are loading", () => {
    const backend = makeBackend({
      listUsers: vi.fn().mockReturnValue(new Promise(() => {})), // never resolves
    });
    renderWithChakra(
      <MemoryRouter>
        <BackendProvider backend={backend}>
          <DialogProvider>
            <UsersPage />
          </DialogProvider>
        </BackendProvider>
      </MemoryRouter>
    );
    expect(screen.getByTestId("list-skeleton")).toBeInTheDocument();
  });
});

describe("UsersPage UX", () => {
  const many = Array.from({ length: 12 }, (_, i) => ({ ...alice, id: `u${i}`, email: `user${i}@example.com` }));

  it("empty state has the only Add user button", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");
    expect(screen.getAllByRole("button", { name: /add user/i })).toHaveLength(1);
  });

  it("shows the toolbar Add user button once there are users", async () => {
    renderUsersPage(makeBackend({ listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }) }));
    await screen.findByText("alice@example.com");
    expect(screen.getAllByRole("button", { name: /add user/i })).toHaveLength(1);
  });

  it("hides search at 10 users or fewer and shows it above 10", async () => {
    const { unmount } = renderUsersPage(makeBackend({ listUsers: vi.fn().mockResolvedValue({ users: many.slice(0, 10), total: 10 }) }));
    await screen.findByText("user0@example.com");
    expect(screen.queryByLabelText("Search users")).toBeNull();
    unmount();
    renderUsersPage(makeBackend({ listUsers: vi.fn().mockResolvedValue({ users: many, total: 12 }) }));
    expect(await screen.findByLabelText("Search users")).toBeInTheDocument();
  });

  it("search filters case-insensitively and reports no matches", async () => {
    renderUsersPage(makeBackend({ listUsers: vi.fn().mockResolvedValue({ users: many, total: 12 }) }));
    const box = await screen.findByLabelText("Search users");
    fireEvent.change(box, { target: { value: "USER11" } });
    expect(screen.getByText("user11@example.com")).toBeInTheDocument();
    expect(screen.queryByText("user0@example.com")).toBeNull();
    fireEvent.change(box, { target: { value: "zzz" } });
    expect(screen.getByText(/no users on this page match/i)).toBeInTheDocument();
  });

  it("a stale query never hides rows once the search box is gone", async () => {
    const listUsers = vi.fn()
      .mockResolvedValueOnce({ users: many, total: 60 })
      .mockResolvedValue({ users: [alice], total: 60 });
    renderUsersPage(makeBackend({ listUsers }));
    fireEvent.change(await screen.findByLabelText("Search users"), { target: { value: "zzz" } });
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("alice@example.com")).toBeInTheDocument();
    expect(screen.queryByLabelText("Search users")).toBeNull();
  });

  it("rows are keyboard-activatable and open the editor", async () => {
    renderUsersPage(makeBackend({ listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }) }));
    const row = await screen.findByRole("button", { name: "Edit alice@example.com" });
    fireEvent.keyDown(row, { key: "Enter" });
    expect(screen.getByText("Edit user")).toBeInTheDocument();
  });

  it("load failure shows a banner with Retry that reloads", async () => {
    const listUsers = vi.fn().mockRejectedValueOnce(new Error("boom")).mockResolvedValue({ users: [alice], total: 1 });
    renderUsersPage(makeBackend({ listUsers }));
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
    fireEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(await screen.findByText("alice@example.com")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("Generate fills a revealed password of sufficient strength", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");
    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.click(screen.getByRole("button", { name: /generate/i }));
    const pw = screen.getByLabelText(/password/i) as HTMLInputElement;
    expect(pw.type).toBe("text");
    expect(pw.value).toHaveLength(16);
    expect(screen.getByText(/^(Good|Strong)$/)).toBeInTheDocument();
  });

  it("flags a too-short password in the hint", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");
    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.change(screen.getByLabelText(/password/i), { target: { value: "abc" } });
    expect(screen.getByText(/too short/i)).toBeInTheDocument();
  });

  it("explains what Confirm email automatically does", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");
    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    expect(screen.getByText(/sign in right away/i)).toBeInTheDocument();
  });

  it("a typed-in modal survives a backdrop click; an untouched one closes", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");
    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.change(screen.getByLabelText(/email/i), { target: { value: "x@y.co" } });
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("cancelling the confirm deletes nothing", async () => {
    const deleteUser = vi.fn();
    renderUsersPage(makeBackend({ listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }), deleteUser }));
    fireEvent.click(await screen.findByText("alice@example.com"));
    fireEvent.click(screen.getByRole("button", { name: /^delete user$/i }));
    await screen.findByText("Delete user?");
    fireEvent.click(screen.getAllByRole("button", { name: "Cancel" }).at(-1)!);
    await waitFor(() => expect(screen.queryByText("Delete user?")).toBeNull());
    expect(deleteUser).not.toHaveBeenCalled();
  });
});
