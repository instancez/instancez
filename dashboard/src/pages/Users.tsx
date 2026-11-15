import { useState, useCallback, useEffect } from "react";
import { Users as UsersIcon, Plus, Check, Minus, ChevronRight, Dices, Search, AlertCircle, RefreshCw } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useBackend } from "../console/BackendContext";
import { useDialog } from "../components/Dialog";
import { ListSkeleton } from "../components/Skeletons";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Modal } from "../components/Modal";
import { Button, Field, Input } from "../components/ui";
import { generatePassword, passwordStrength } from "../lib/password";
import { Toggle } from "../components/Toggle";
import type { AdminUser } from "../lib/types";

function formatDate(iso: string): string {
  if (!iso) return "Never";
  return new Date(iso).toLocaleDateString();
}

function isBanned(user: AdminUser): boolean {
  return !!user.banned_until;
}

function isValidEmail(email: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim());
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
  const [showPassword, setShowPassword] = useState(false);
  const strength = passwordStrength(password);
  const dirty = isEdit
    ? email !== user!.email || password !== "" || banned !== isBanned(user!)
    : email !== "" || password !== "" || emailConfirm;

  function fillGenerated() {
    setPassword(generatePassword());
    setShowPassword(true);
  }

  async function handleSubmit() {
    setError("");
    setSaving(true);
    try {
      if (isEdit) {
        const patch: { email?: string; password?: string; ban_duration?: string } = {};
        if (email !== user!.email) {
          if (!isValidEmail(email)) { setError("Enter a valid email address."); setSaving(false); return; }
          patch.email = email.trim();
        }
        if (password) {
          if (password.length < 6) { setError("Password must be at least 6 characters."); setSaving(false); return; }
          patch.password = password;
        }
        const wasBanned = isBanned(user!);
        if (banned !== wasBanned) patch.ban_duration = banned ? "infinity" : "none";
        if (Object.keys(patch).length === 0) {
          onClose();
          return;
        }
        await backend.updateUser(user!.id, patch);
      } else {
        if (!email) { setError("Email is required."); setSaving(false); return; }
        if (!isValidEmail(email)) { setError("Enter a valid email address."); setSaving(false); return; }
        if (!password) { setError("Password is required."); setSaving(false); return; }
        if (password.length < 6) { setError("Password must be at least 6 characters."); setSaving(false); return; }
        await backend.createUser(email.trim(), password, emailConfirm);
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
    const ok = await dialog.confirm("Delete user?", {
      message: `${user.email} and all their sessions will be removed. This can't be undone.`,
      confirmText: user.email,
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
    <Modal open onClose={onClose} title={isEdit ? "Edit user" : "Add user"} dirty={dirty}>
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
          <Field
            label={isEdit ? "New password" : "Password"}
            htmlFor="user-password"
            hint={strength.label ? <Text as="span" color={strength.level === "short" ? "fg.error" : "fg.muted"}>{strength.label}</Text> : "At least 6 characters"}
          >
            <HStack gap="2" w="full">
              <Input
                id="user-password"
                type={showPassword ? "text" : "password"}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={isEdit ? "Leave blank to keep current" : "Password"}
                autoComplete="new-password"
              />
              <Button variant="outline" onClick={fillGenerated}>
                <Dices size={14} />
                Generate
              </Button>
            </HStack>
          </Field>
          {!isEdit && (
            <VStack gap="1" align="stretch">
              <Toggle
                checked={emailConfirm}
                onChange={setEmailConfirm}
                label="Confirm email automatically"
              />
              <Text fontSize="xs" color="fg.muted" pl="12">
                Off: the user must click the emailed link first.
              </Text>
            </VStack>
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
            <Button variant="danger-outline" size="sm" onClick={handleDelete} disabled={saving}>
              Delete user
            </Button>
          </Box>
        )}
        <Button variant="ghost" onClick={onClose} disabled={saving}>
          Cancel
        </Button>
        <Button onClick={handleSubmit} loading={saving}>
          {isEdit ? "Save" : "Create"}
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
  const [query, setQuery] = useState("");

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

  const searchable = users.length > 10;
  const q = searchable ? query.trim().toLowerCase() : "";
  const shown = q ? users.filter((u) => u.email.toLowerCase().includes(q)) : users;
  const addButton = (
    <Button onClick={openCreate}>
      <Plus size={14} />
      Add user
    </Button>
  );

  return (
    <Box pb="20">
      <HStack justify="space-between" gap="4" pb="6">
        <Text fontSize="sm" color="fg.muted">
          {loading ? "Loading…" : `${total} user${total !== 1 ? "s" : ""}`}
        </Text>
        {total > 0 && addButton}
      </HStack>

      {loadError && (
        <HStack role="alert" gap="3" mb="4" px="4" py="3" borderWidth="1px" borderColor="border.error" borderRadius="lg" fontSize="sm" color="fg.error">
          <Box as={AlertCircle} boxSize="4" flexShrink="0" />
          <Text flex="1">{loadError}</Text>
          <Button variant="outline" size="sm" onClick={() => void load(page)}>
            <RefreshCw size={13} />
            Retry
          </Button>
        </HStack>
      )}

      {loading && users.length === 0 && <ListSkeleton rows={6} />}

      {!loading && users.length === 0 && !loadError && (
        <EmptyState
          icon={UsersIcon}
          title="No users yet"
          description="Create your first user to get started."
          action={addButton}
        />
      )}

      {searchable && (
        <Box position="relative" maxW="sm" pb="4">
          <Box as={Search} position="absolute" left="3" top="2.5" boxSize="3.5" color="fg.muted" pointerEvents="none" />
          <Input
            aria-label="Search users"
            placeholder="Search by email"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            style={{ paddingLeft: "2rem" }}
          />
        </Box>
      )}

      {q && shown.length === 0 && (
        <Text fontSize="sm" color="fg.muted" pb="4">No users on this page match "{query.trim()}".</Text>
      )}

      {shown.length > 0 && (
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
            <Box w="4" />
          </HStack>
          {shown.map((user) => (
            <HStack
              key={user.id}
              role="button"
              tabIndex={0}
              aria-label={`Edit ${user.email}`}
              onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); openEdit(user); } }}
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
              <Box as={ChevronRight} boxSize="4" color="fg.muted" flexShrink="0" />
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
