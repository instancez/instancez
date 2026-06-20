import {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  useRef,
  type ReactNode,
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

export function useConfig(): ConfigState {
  const ctx = useContext(ConfigContext);
  if (!ctx) throw new Error("useConfig must be used within ConfigProvider");
  return ctx;
}

export function useConfigState(initialConfig?: Config | null): ConfigStateWithDialog {
  const backend = useBackend();
  const [config, setConfig] = useState<Config | null>(initialConfig ?? null);
  const [loading, setLoading] = useState(initialConfig == null);
  const [error, setError] = useState<string | null>(null);
  const [checksum, setChecksum] = useState(initialConfig?._checksum ?? "");
  const [saving, setSaving] = useState(false);
  const [saveErrors, setSaveErrors] = useState<ValidationError[]>([]);
  const [dotenvWritable, setDotenvWritable] = useState(false);
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
      setConfig(cfg);
      setDotenvWritable(status?.dotenv_writable ?? false);
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
        .then((s) => setDotenvWritable(s?.dotenv_writable ?? false))
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
    refresh,
    save,
    updateConfig,
    pendingSave,
    confirmPendingSave,
    cancelPendingSave,
  };
}
