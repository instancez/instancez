import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect } from "react";
import { ArrowLeft, Trash2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { CodeEditor } from "../components/CodeEditor";
import { TagInput } from "../components/TagInput";
import type { Trigger } from "../lib/types";

export function EventDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [trigger, setTrigger] = useState<Trigger | null>(null);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (config && name && config.on[name]) {
      setTrigger(structuredClone(config.on[name]!));
      setDirty(false);
    }
  }, [config, name]);

  function updateTrigger(updater: (prev: Trigger) => Trigger) {
    setTrigger((prev) => {
      if (!prev) return prev;
      setDirty(true);
      return updater(prev);
    });
  }

  async function handleSave() {
    if (!config || !trigger || !name) return;
    const updated = {
      ...config,
      on: { ...config.on, [name]: trigger },
    };
    await save(updated);
    setDirty(false);
  }

  async function deleteTrigger() {
    if (!config || !name) return;
    if (!(await dialog.confirm(`Delete trigger "${name}"?`, { message: "This will remove the trigger and stop all event delivery.", confirmText: name }))) return;
    const { [name]: _, ...rest } = config.on;
    const updated = { ...config, on: rest };
    const ok = await save(updated);
    if (ok) navigate("/events");
  }

  if (!config || !trigger || !name) {
    return (
      <div className="p-8">
        <p className="text-sm text-muted-foreground">Trigger not found.</p>
      </div>
    );
  }

  // Build event autocomplete from tables
  const eventSuggestions: string[] = [];
  for (const tName of Object.keys(config.tables || {})) {
    for (const op of ["insert", "update", "delete"]) {
      eventSuggestions.push(`${tName}.${op}`);
    }
  }
  eventSuggestions.push("*.insert", "*.update", "*.delete");

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description="Event trigger configuration"
        actions={
          <div className="flex items-center gap-2">
            <button
              onClick={() => navigate("/events")}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
            >
              <ArrowLeft size={14} />
              Back
            </button>
            <button
              onClick={deleteTrigger}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-destructive/30 text-sm text-destructive hover:bg-destructive/10 transition-colors cursor-pointer"
            >
              <Trash2 size={14} />
              Delete
            </button>
          </div>
        }
      />

      <div className="px-8 space-y-6 max-w-2xl">
        <div>
          <label className="block text-sm font-medium text-foreground mb-1">Events</label>
          <TagInput
            value={trigger.events || []}
            onChange={(evts) => updateTrigger((t) => ({ ...t, events: evts }))}
            suggestions={eventSuggestions}
            placeholder="e.g. todos.insert, *.delete"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-foreground mb-1">
            Cron Schedule (optional)
          </label>
          <input
            type="text"
            value={trigger.schedule || ""}
            onChange={(e) => updateTrigger((t) => ({ ...t, schedule: e.target.value }))}
            placeholder="0 9 * * * (every day at 9 AM)"
            className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
          />
        </div>

        {/* Webhook Config */}
        {trigger.webhook && (
          <section className="space-y-3 p-4 rounded-xl border border-border bg-surface">
            <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
              Webhook
            </h3>
            <div>
              <label className="block text-xs text-muted-foreground mb-1">URL</label>
              <input
                type="text"
                value={trigger.webhook.url}
                onChange={(e) =>
                  updateTrigger((t) => ({
                    ...t,
                    webhook: { ...t.webhook!, url: e.target.value },
                  }))
                }
                placeholder="https://example.com/webhook"
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Retry Max</label>
                <input
                  type="number"
                  value={trigger.webhook.retry?.max ?? 3}
                  onChange={(e) =>
                    updateTrigger((t) => ({
                      ...t,
                      webhook: {
                        ...t.webhook!,
                        retry: { ...t.webhook!.retry, max: parseInt(e.target.value) || 0 },
                      },
                    }))
                  }
                  className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors"
                />
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Backoff</label>
                <select
                  value={trigger.webhook.retry?.backoff || "exponential"}
                  onChange={(e) =>
                    updateTrigger((t) => ({
                      ...t,
                      webhook: {
                        ...t.webhook!,
                        retry: { ...t.webhook!.retry, backoff: e.target.value },
                      },
                    }))
                  }
                  className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
                >
                  <option value="exponential">Exponential</option>
                  <option value="linear">Linear</option>
                </select>
              </div>
            </div>
          </section>
        )}

        {/* Email Config */}
        {trigger.email && (
          <section className="space-y-3 p-4 rounded-xl border border-border bg-surface">
            <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
              Email Action
            </h3>
            <div>
              <label className="block text-xs text-muted-foreground mb-1">To</label>
              <input
                type="text"
                value={trigger.email.to}
                onChange={(e) =>
                  updateTrigger((t) => ({
                    ...t,
                    email: { ...t.email!, to: e.target.value },
                  }))
                }
                placeholder='{{data.email}} or literal address'
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
              />
            </div>
            <div>
              <label className="block text-xs text-muted-foreground mb-1">Subject</label>
              <input
                type="text"
                value={trigger.email.subject}
                onChange={(e) =>
                  updateTrigger((t) => ({
                    ...t,
                    email: { ...t.email!, subject: e.target.value },
                  }))
                }
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
              />
            </div>
            <div>
              <label className="block text-xs text-muted-foreground mb-1">Body</label>
              <CodeEditor
                value={trigger.email.body}
                onChange={(val) =>
                  updateTrigger((t) => ({
                    ...t,
                    email: { ...t.email!, body: val },
                  }))
                }
                language="text"
                minHeight="100px"
              />
            </div>
          </section>
        )}
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
