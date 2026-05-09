import { useEffect, useState } from "react";
import { getConfigStatus } from "../api/client";
import type { ConfigStatus } from "../lib/types";

type State = {
  data: ConfigStatus | null;
  error: Error | null;
};

export function useConfigStatus(intervalMs: number = 5000): State {
  const [state, setState] = useState<State>({ data: null, error: null });

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const tick = async () => {
      try {
        const data = await getConfigStatus();
        if (!cancelled) setState({ data, error: null });
      } catch (err) {
        if (!cancelled) setState((prev) => ({ data: prev.data, error: err as Error }));
      }
      if (!cancelled) {
        timer = setTimeout(tick, intervalMs);
      }
    };
    tick();

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [intervalMs]);

  return state;
}
