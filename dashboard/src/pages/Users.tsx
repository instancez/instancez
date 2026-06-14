import { useState, useCallback, useEffect } from "react";
import { Users as UsersIcon, Plus, Check, Minus } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useBackend } from "../console/BackendContext";
import { useDialog } from "../components/Dialog";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Modal } from "../components/Modal";
import { Button, Field, Input } from "../components/ui";
import { Toggle } from "../components/Toggle";
import type { AdminUser } from "../lib/types";

function formatDate(iso: string): string {
  if (!iso) return "Never";
  return new Date(iso).toLocaleDateString();
}

function isBanned(user: AdminUser): boolean {
  return !!user.banned_until;
}

interface UserModalProps {
  user: AdminUser | null;
  onClose: () => void;
  onSaved: () => void;
}

function UserModal({ user, onClose, onSaved }: UserModalProps) {
  const backend = useBackend();
  const dialog = useDialog();
  const isEdit = user !== null;

  const [email, setEmail] = useState(user?.email ?? "");
  const [password, setPassword] = useState("");
  const [emailConfirm, setEmailConfirm] = useState(false);
  const [banned, setBanned] = useState(isEdit ? isBanned(user!) : false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  async function handleSubmit() {
    setError("");
    setSaving(true);
    try {
      if (isEdit) {
        const patch: { email?: string; password?: string; ban_duration?: string } = {};
        if (email !== user!.email) patch.email = email;
        if (password) patch.password = password;
        const wasBanned = isBanned(user!);
        if (banned !== wasBanned) patch.ban_duration = banned ? "infinity" : "none";
        if (Object.keys(patch).length === 0) {
          onClose();
          return;
        }
        await backend.updateUser(user!.id, patch);
      } else {
        if (!email) {
          setError("Email is required.");
          setSaving(false);
          return;
        }
        if (!password) {
          setError("Password is required.");
          setSaving(false);
          return;
        }
        await backend.createUser(email, password, emailConfirm);
      }
      onSaved();
      onClose();
    } catch (err: any) {
      setError(err?.body?.message || err?.message || "An error occurred.");
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!user) return;
    const ok = await dialog.confirm(`Delete user "${user.email}"?`, {
      message: "This will permanently remove the user and all their sessions.",
      confirmText: user.email,
      confirmLabel: "Delete user",
    });
    if (!ok) return;
    setSaving(true);
    try {
      await backend.deleteUser(user.id);
      onSaved();
      onClose();
    } catch (err: any) {
      setError(err?.body?.message || err?.message || "Failed to delete user.");
      setSaving(false);
    }
  }

  return (
    <Modal open onClose={onClose} title={isEdit ? "Edit user" : "Add user"}>
      <Modal.Body>
        <VStack gap="4" align="stretch">
          <Field label="Email" htmlFor="user-email">
            <Input
              id="user-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="user@example.com"
            />
          </Field>
          <Field label={isEdit ? "New password" : "Password"} htmlFor="user-password">
            <Input
              id="user-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={isEdit ? "Leave blank to keep current" : "Password"}
            />
          </Field>
          {!isEdit && (
            <Toggle
              checked={emailConfirm}
              onChange={setEmailConfirm}
              label="Confirm email automatically"
            />
          )}
          {isEdit && (
            <Toggle
              checked={banned}
              onChange={setBanned}
              label="Banned"
            />
          )}
          {error && (
            <Text fontSize="xs" color="fg.error">
              {error}
            </Text>
          )}
        </VStack>
      </Modal.Body>
      <Modal.Footer>
        {isEdit && (
          <Box flex="1">
            <Button variant="danger-ghost" size="sm" onClick={handleDelete} disabled={saving}>
              Delete
            </Button>
          </Box>
        )}
        <Button variant="ghost" onClick={onClose} disabled={saving}>
          Cancel
        </Button>
        <Button onClick={handleSubmit} loading={saving}>
          {isEdit ? "Save" : "Create user"}
        </Button>
      </Modal.Footer>
    </Modal>
  );
}

export function UsersPage() {
  const backend = useBackend();
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [modalOpen, setModalOpen] = useState(false);
  const [selectedUser, setSelectedUser] = useState<AdminUser | null>(null);

  const perPage = 50;

  const load = useCallback(
    async (p: number) => {
      setLoading(true);
      setLoadError("");
      try {
        const result = await backend.listUsers(p, perPage);
        setUsers(result.users);
        setTotal(result.total);
      } catch (err: any) {
        setLoadError(err?.message || "Failed to load users.");
      } finally {
        setLoading(false);
      }
    },
    [backend]
  );

  useEffect(() => {
    load(page);
  }, [load, page]);

  function openCreate() {
    setSelectedUser(null);
    setModalOpen(true);
  }

  function openEdit(user: AdminUser) {
    setSelectedUser(user);
    setModalOpen(true);
  }

  function closeModal() {
    setModalOpen(false);
    setSelectedUser(null);
  }

  function onSaved() {
    load(page);
  }

  return (
    <Box pb="20">
      <HStack justify="space-between" gap="4" pb="6">
        <Text fontSize="sm" color="fg.muted">
          {loading ? "Loading…" : `${total} user${total !== 1 ? "s" : ""}`}
        </Text>
        <Button onClick={openCreate}>
          <Plus size={14} />
          Add user
        </Button>
      </HStack>

      {loadError && (
        <Text fontSize="sm" color="fg.error" pb="4">
          {loadError}
        </Text>
      )}

      {!loading && users.length === 0 && !loadError && (
        <EmptyState
          icon={UsersIcon}
          title="No users yet"
          description="Create your first user to get started."
        />
      )}

      {users.length > 0 && (
        <VStack gap="0" align="stretch" borderWidth="1px" borderRadius="xl" overflow="hidden">
          <HStack
            px="4"
            py="2"
            bg="bg.subtle"
            borderBottomWidth="1px"
            borderColor="border"
            fontSize="xs"
            fontWeight="medium"
            color="fg.muted"
          >
            <Text flex="1">Email</Text>
            <Text w="24" textAlign="center">
              Confirmed
            </Text>
            <Text w="32">Last sign-in</Text>
            <Text w="28">Created</Text>
            <Text w="20" textAlign="center">
              Status
            </Text>
          </HStack>
          {users.map((user) => (
            <HStack
              key={user.id}
              px="4"
              py="3"
              borderBottomWidth="1px"
              borderColor="border"
              _last={{ borderBottomWidth: 0 }}
              cursor="pointer"
              _hover={{ bg: "bg.subtle" }}
              onClick={() => openEdit(user)}
              fontSize="sm"
            >
              <Text flex="1" color="fg" fontFamily="mono" fontSize="xs">
                {user.email}
              </Text>
              <Box w="24" display="flex" justifyContent="center">
                {user.email_confirmed_at ? (
                  <Box as={Check} boxSize="3.5" color="green.500" />
                ) : (
                  <Box as={Minus} boxSize="3.5" color="fg.muted" />
                )}
              </Box>
              <Text w="32" color="fg.muted" fontSize="xs">
                {formatDate(user.last_sign_in_at)}
              </Text>
              <Text w="28" color="fg.muted" fontSize="xs">
                {formatDate(user.created_at)}
              </Text>
              <Box w="20" display="flex" justifyContent="center">
                {isBanned(user) && <StatusBadge variant="error">Banned</StatusBadge>}
              </Box>
            </HStack>
          ))}
        </VStack>
      )}

      {total > perPage && (
        <HStack justify="center" gap="2" pt="6">
          <Button
            variant="outline"
            size="sm"
            disabled={page === 1}
            onClick={() => setPage((p) => p - 1)}
          >
            Previous
          </Button>
          <Text fontSize="sm" color="fg.muted">
            Page {page} of {Math.ceil(total / perPage)}
          </Text>
          <Button
            variant="outline"
            size="sm"
            disabled={page >= Math.ceil(total / perPage)}
            onClick={() => setPage((p) => p + 1)}
          >
            Next
          </Button>
        </HStack>
      )}

      {modalOpen && (
        <UserModal user={selectedUser} onClose={closeModal} onSaved={onSaved} />
      )}
    </Box>
  );
}
