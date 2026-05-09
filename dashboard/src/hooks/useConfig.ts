import {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  type ReactNode,
} from "react";
import { getConfig, putConfig } from "../api/client";
import { showSaveToast } from "../components/SaveToast";
import type { Config, ValidationError } from "../lib/types";

interface ConfigState {
  config: Config | null;
  loading: boolean;
  error: string | null;
  checksum: string;
  saving: boolean;
  saveErrors: ValidationError[];
  refresh: () => Promise<void>;
  save: (updated: Config) => Promise<boolean>;
  updateConfig: (updater: (prev: Config) => Config) => void;
}

const ConfigContext = createContext<ConfigState | null>(null);

export { ConfigContext };

export function useConfig(): ConfigState {
  const ctx = useContext(ConfigContext);
  if (!ctx) throw new Error("useConfig must be used within ConfigProvider");
  return ctx;
}

export function useConfigState(): ConfigState {
  const [config, setConfig] = useState<Config | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [checksum, setChecksum] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveErrors, setSaveErrors] = useState<ValidationError[]>([]);

  const refresh = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const cfg = await getConfig();
      setChecksum(cfg._checksum || "");
      setConfig(cfg);
    } catch (e: any) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, []);

  const save = useCallback(
    async (updated: Config): Promise<boolean> => {
      try {
        setSaving(true);
        setSaveErrors([]);
        const { _checksum, ...body } = updated;
        const resp = await putConfig(body, checksum);
        showSaveToast({
          source: resp.config_source ?? "",
          statementCount: 0,
        });
        await refresh();
        return true;
      } catch (e: any) {
        if (e.body?.errors) {
          setSaveErrors(e.body.errors);
        } else {
          setSaveErrors([{ path: "", message: e.message }]);
        }
        return false;
      } finally {
        setSaving(false);
      }
    },
    [checksum, refresh]
  );

  const updateConfig = useCallback(
    (updater: (prev: Config) => Config) => {
      setConfig((prev) => (prev ? updater(prev) : prev));
    },
    []
  );

  useEffect(() => {
    refresh();
  }, [refresh]);

  return {
    config,
    loading,
    error,
    checksum,
    saving,
    saveErrors,
    refresh,
    save,
    updateConfig,
  };
}
