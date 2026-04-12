import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import * as Tabs from "@radix-ui/react-tabs";
import {
  Plus,
  Zap,
  RotateCcw,
  Loader2,
} from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { getEvents, retryEvent, purgeEvents } from "../api/client";
import { PageHeader } from "../components/PageHeader";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { timeAgo } from "../lib/utils";
import type { EventRow } from "../lib/types";

export function Events() {
  const navigate = useNavigate();
  const { config, save } = useConfig();
  const dialog = useDialog();

  // Event log state
  const [events, setEvents] = useState<EventRow[]>([]);
  const [eventsLoading, setEventsLoading] = useState(false);
  const [statusFilter, setStatusFilter] = useState<string>("");

  async function addTrigger() {
    const name = await dialog.prompt("Trigger name:");
    if (!name?.trim()) return;
    const triggerName = name.trim().toLowerCase().replace(/\s+/g, "_");

    const updated = {
      ...config!,
      on: {
        ...config!.on,
        [triggerName]: {
          events: [],
          schedule: "",
          webhook: { url: "", headers: {}, retry: { max: 3, backoff: "exponential" } },
          email: null,
        },
      },
    };

    const ok = await save(updated);
    if (ok) navigate(`/events/${triggerName}`);
  }

  async function loadEvents() {
    setEventsLoading(true);
    try {
      const data = await getEvents(statusFilter || undefined);
      setEvents(data || []);
    } catch {
      // ignore
    } finally {
      setEventsLoading(false);
    }
  }

  async function handleRetry(id: string) {
    await retryEvent(id);
    loadEvents();
  }

  async function handlePurge() {
    if (!(await dialog.confirm("Purge all delivered events older than 7 days?"))) return;
    await purgeEvents();
    loadEvents();
  }

  useEffect(() => {
    loadEvents();
  }, [statusFilter]);

  if (!config) return null;

  const triggers = Object.entries(config.on || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  const STATUS_VARIANT: Record<string, "success" | "error" | "warning" | "info" | "muted"> = {
    delivered: "success",
    pending: "info",
    failed: "warning",
    dead: "error",
  };

  return (
    <div>
      <PageHeader
        title="Events"
        description={`${triggers.length} trigger${triggers.length !== 1 ? "s" : ""} configured`}
        actions={
          <button
            onClick={addTrigger}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
          >
            <Plus size={14} />
            Add Trigger
          </button>
        }
      />

      <div className="px-8">
        <Tabs.Root defaultValue="triggers">
          <Tabs.List className="flex gap-1 border-b border-border mb-6">
            <Tabs.Trigger
              value="triggers"
              className="px-4 py-2 text-sm font-medium text-muted-foreground data-[state=active]:text-accent data-[state=active]:border-b-2 data-[state=active]:border-accent -mb-px transition-colors cursor-pointer hover:text-foreground"
            >
              Triggers
            </Tabs.Trigger>
            <Tabs.Trigger
              value="log"
              className="px-4 py-2 text-sm font-medium text-muted-foreground data-[state=active]:text-accent data-[state=active]:border-b-2 data-[state=active]:border-accent -mb-px transition-colors cursor-pointer hover:text-foreground"
            >
              Event Log
            </Tabs.Trigger>
          </Tabs.List>

          {/* Triggers Tab */}
          <Tabs.Content value="triggers">
            {triggers.length === 0 ? (
              <EmptyState
                icon={Zap}
                title="No triggers yet"
                description="Create event triggers to react to database changes."
                action={
                  <button
                    onClick={addTrigger}
                    className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
                  >
                    <Plus size={14} />
                    Add Trigger
                  </button>
                }
              />
            ) : (
              <div className="space-y-2">
                {triggers.map(([name, t]) => {
                  const isWebhook = !!t.webhook;
                  const isEmail = !!t.email;
                  const isCron = !!t.schedule;
                  const eventCount = (t.events || []).length;

                  return (
                    <button
                      key={name}
                      onClick={() => navigate(`/events/${name}`)}
                      className="w-full flex items-center justify-between px-5 py-3.5 rounded-lg border border-border bg-surface hover:bg-surface-hover hover:border-border-hover transition-colors cursor-pointer text-left group"
                    >
                      <div className="flex items-center gap-3">
                        <Zap
                          size={16}
                          className="text-muted-foreground group-hover:text-foreground transition-colors"
                        />
                        <span className="text-sm font-mono font-medium text-foreground">
                          {name}
                        </span>
                      </div>
                      <div className="flex items-center gap-2">
                        {eventCount > 0 && (
                          <StatusBadge variant="muted">
                            {eventCount} event{eventCount !== 1 ? "s" : ""}
                          </StatusBadge>
                        )}
                        {isWebhook && (
                          <StatusBadge variant="info">webhook</StatusBadge>
                        )}
                        {isEmail && (
                          <StatusBadge variant="info">email</StatusBadge>
                        )}
                        {isCron && (
                          <StatusBadge variant="warning">cron</StatusBadge>
                        )}
                      </div>
                    </button>
                  );
                })}
              </div>
            )}
          </Tabs.Content>

          {/* Event Log Tab */}
          <Tabs.Content value="log">
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <select
                    value={statusFilter}
                    onChange={(e) => setStatusFilter(e.target.value)}
                    className="px-3 py-1.5 rounded-lg border border-border bg-input text-sm text-foreground cursor-pointer focus:outline-none focus:border-ring"
                  >
                    <option value="">All statuses</option>
                    <option value="delivered">Delivered</option>
                    <option value="pending">Pending</option>
                    <option value="failed">Failed</option>
                    <option value="dead">Dead</option>
                  </select>
                  <button
                    onClick={loadEvents}
                    disabled={eventsLoading}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
                  >
                    <RotateCcw size={13} className={eventsLoading ? "animate-spin" : ""} />
                    Refresh
                  </button>
                </div>
                <button
                  onClick={handlePurge}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-destructive/30 text-xs text-destructive hover:bg-destructive/10 transition-colors cursor-pointer"
                >
                  Purge Dead
                </button>
              </div>

              {eventsLoading ? (
                <div className="flex justify-center py-8">
                  <Loader2 size={20} className="animate-spin text-muted-foreground" />
                </div>
              ) : events.length === 0 ? (
                <EmptyState
                  icon={Zap}
                  title="No events"
                  description="Events will appear here as they are processed."
                />
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-border">
                        <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">Event</th>
                        <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">Table</th>
                        <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">Operation</th>
                        <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">Status</th>
                        <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">Attempts</th>
                        <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">Time</th>
                        <th className="w-10" />
                      </tr>
                    </thead>
                    <tbody>
                      {events.map((evt) => (
                        <tr key={evt.id} className="border-b border-border/50 hover:bg-surface-hover/30">
                          <td className="px-3 py-2 font-mono text-foreground">{evt.event}</td>
                          <td className="px-3 py-2 font-mono text-muted-foreground">{evt.table}</td>
                          <td className="px-3 py-2 text-muted-foreground">{evt.operation}</td>
                          <td className="px-3 py-2">
                            <StatusBadge variant={STATUS_VARIANT[evt.status] || "muted"} dot>
                              {evt.status}
                            </StatusBadge>
                          </td>
                          <td className="px-3 py-2 tabular-nums text-muted-foreground">{evt.attempts}</td>
                          <td className="px-3 py-2 text-xs text-muted-foreground">
                            {evt.created_at ? timeAgo(evt.created_at) : "—"}
                          </td>
                          <td className="px-2 py-2">
                            {(evt.status === "failed" || evt.status === "dead") && (
                              <button
                                onClick={() => handleRetry(evt.id)}
                                className="p-1 rounded hover:bg-accent/10 text-muted-foreground hover:text-accent transition-colors cursor-pointer"
                                title="Retry"
                              >
                                <RotateCcw size={13} />
                              </button>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </Tabs.Content>
        </Tabs.Root>
      </div>
    </div>
  );
}
