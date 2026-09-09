import {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  useRef,
} from "react";
import { useBackend } from "../console/BackendContext";
import { showSaveToast, showSaveErrorToast } from "../components/SaveToast";
import type { DotenvChange } from "../components/ConfirmSaveDialog";
import type { Config, ValidationError } from "../lib/types";

export interface SaveOptions {
  /** Staged .env writes shown (masked) in the save-confirmation dialog. */
  dotenvChanges?: DotenvChange[];
}

export interface PendingSave {
  current: string;
  proposed: string;
  dotenvChanges: DotenvChange[];
}

interface ConfigState {
  config: Config | null;
  loading: boolean;
  error: string | null;
  checksum: string;
  saving: boolean;
  saveErrors: ValidationError[];
  dotenvWritable: boolean;
  oauthCallbackBase: string;
  refresh: () => Promise<void>;
  save: (updated: Config, opts?: SaveOptions) => Promise<boolean>;
  updateConfig: (updater: (prev: Config) => Config) => void;
}

/**
 * The full state returned by useConfigState. The save-confirmation dialog
 * fields stay off the ConfigState context type so page-level consumers (and
 * their test mocks) only see the save() API; Layout renders the dialog.
 */
export interface ConfigStateWithDialog extends ConfigState {
  pendingSave: PendingSave | null;
  confirmPendingSave: () => void;
  cancelPendingSave: () => void;
}

const ConfigContext = createContext<ConfigState | null>(null);

export { ConfigContext };

// Both backends (engine + platform) serialize empty Go maps/slices as JSON
// null, but the Config type marks these collections required and every tab
// iterates them directly (Object.entries / .map / .length). Coerce null → the
// empty collection at this single seam — the one place both the OSS refresh and
// the platform-seeded config converge — so no tab, current or future, crashes.
// Genuinely-nullable fields (auth, providers.*, oauth values, foreign_key,
// min/max) are left untouched.
// The `?? []`/`?? {}` live in these helpers so the coalesce operand is a
// genuinely nullable type — the Config type marks the collections non-null
// (that's the lie we're correcting), so an inline `x.fields ?? []` reads as
// dead code to the type-aware linter.
const orArr = <V,>(v: V[] | null | undefined): V[] => v ?? [];
const orMap = <V,>(m: Record<string, V> | null | undefined): Record<string, V> => m ?? {};
const mapVals = <V,>(m: Record<string, V> | null | undefined, fn: (v: V) => V): Record<string, V> =>
  Object.fromEntries(Object.entries(orMap(m)).map(([k, v]) => [k, fn(v)]));

function normalizeConfig<T extends Config | null>(cfg: T): T {
  if (!cfg) return cfg;
  const auth = cfg.auth;
  return {
    ...cfg,
    tables: mapVals(cfg.tables, (t) => ({
      ...t,
      fields: orArr(t.fields),
      indexes: orArr(t.indexes),
      rls: orArr(t.rls),
    })),
    storage: mapVals(cfg.storage, (b) => ({ ...b, types: orArr(b.types), rls: orArr(b.rls) })),
    rpc: mapVals(cfg.rpc, (r) => ({ ...r, args: orArr(r.args) })),
    functions: orMap(cfg.functions),
    auth: auth
      ? {
          ...auth,
          redirect_urls: orArr(auth.redirect_urls),
          oauth: orMap(auth.oauth),
          email: auth.email ? { ...auth.email, templates: orMap(auth.email.templates) } : auth.email,
        }
      : auth,
  } as T;
}

export function useConfig(): ConfigState {
  const ctx = useContext(ConfigContext);
  if (!ctx) throw new Error("useConfig must be used within ConfigProvider");
  return ctx;
}

export function useConfigState(initialConfig?: Config | null): ConfigStateWithDialog {
  const backend = useBackend();
  const [config, setConfig] = useState<Config | null>(normalizeConfig(initialConfig ?? null));
  const [loading, setLoading] = useState(initialConfig == null);
  const [error, setError] = useState<string | null>(null);
  const [checksum, setChecksum] = useState(initialConfig?._checksum ?? "");
  const [saving, setSaving] = useState(false);
  const [saveErrors, setSaveErrors] = useState<ValidationError[]>([]);
  const [dotenvWritable, setDotenvWritable] = useState(false);
  const [oauthCallbackBase, setOauthCallbackBase] = useState("");
  const [pendingSave, setPendingSave] = useState<PendingSave | null>(null);
  const pendingResolve = useRef<((confirmed: boolean) => void) | null>(null);

  const refresh = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const [cfg, status] = await Promise.all([
        backend.getConfig(),
        backend.getConfigStatus().catch(() => null),
      ]);
      setChecksum(cfg._checksum || "");
      setConfig(normalizeConfig(cfg));
      setDotenvWritable(status?.dotenv_writable ?? false);
      setOauthCallbackBase(status?.oauth_callback_base ?? "");
    } catch (e: any) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, [backend]);

  const confirmPendingSave = useCallback(() => {
    pendingResolve.current?.(true);
    pendingResolve.current = null;
  }, []);

  const cancelPendingSave = useCallback(() => {
    pendingResolve.current?.(false);
    pendingResolve.current = null;
    setPendingSave(null);
  }, []);

  const save = useCallback(
    async (updated: Config, opts?: SaveOptions): Promise<boolean> => {
      setSaveErrors([]);
      const { _checksum, ...body } = updated;

      // Dry-run first: what would each file look like after this save?
      let preview;
      try {
        preview = await backend.previewConfig(body);
      } catch (e: any) {
        if (e.body?.errors) {
          setSaveErrors(e.body.errors);
        } else {
          setSaveErrors([{ path: "", message: e.message }]);
        }
        const msg = e.body?.errors
          ? `Couldn't save: ${e.body.errors.length} validation error${e.body.errors.length === 1 ? "" : "s"}`
          : e.message || "Couldn't save";
        showSaveErrorToast({ message: msg });
        return false;
      }

      // Hold the save until the user confirms the per-file summary.
      const confirmed = await new Promise<boolean>((resolve) => {
        pendingResolve.current = resolve;
        setPendingSave({
          current: preview.current,
          proposed: preview.proposed,
          dotenvChanges: opts?.dotenvChanges ?? [],
        });
      });
      if (!confirmed) return false;

      try {
        setSaving(true);
        const resp = await backend.putConfig(body, checksum);
        showSaveToast({ source: resp.config_source ?? "" });
        await refresh();
        return true;
      } catch (e: any) {
        if (e.body?.errors) {
          setSaveErrors(e.body.errors);
        } else {
          setSaveErrors([{ path: "", message: e.message }]);
        }
        const msg = e.body?.errors
          ? `Couldn't save: ${e.body.errors.length} validation error${e.body.errors.length === 1 ? "" : "s"}`
          : e.message || "Couldn't save";
        showSaveErrorToast({ message: msg });
        return false;
      } finally {
        setSaving(false);
        setPendingSave(null);
      }
    },
    [backend, checksum, refresh]
  );

  const updateConfig = useCallback(
    (updater: (prev: Config) => Config) => {
      setConfig((prev) => (prev ? updater(prev) : prev));
    },
    []
  );

  useEffect(() => {
    // Seeded by the host: config is already in state, so skip the redundant
    // getConfig() and only refresh the lightweight status (drives dotenvWritable).
    // Unseeded (OSS dashboard): full refresh as before.
    if (initialConfig != null) {
      backend
        .getConfigStatus()
        .then((s) => {
          setDotenvWritable(s?.dotenv_writable ?? false);
          setOauthCallbackBase(s?.oauth_callback_base ?? "");
        })
        .catch(() => {});
      return;
    }
    refresh();
    // initialConfig is a one-time seed; intentionally not in deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refresh, backend]);

  return {
    config,
    loading,
    error,
    checksum,
    saving,
    saveErrors,
    dotenvWritable,
    oauthCallbackBase,
    refresh,
    save,
    updateConfig,
    pendingSave,
    confirmPendingSave,
    cancelPendingSave,
  };
}
