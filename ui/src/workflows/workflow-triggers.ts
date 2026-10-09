import { computed, onMounted, ref, type Ref } from "vue";
import { parse } from "yaml";

import { api } from "@/lib/api";
import { branchingOf } from "./workflow-edit.ts";
import { useControlPlaneStore } from "@/stores/control-plane";
import type { Binding } from "@/bindings/binding-draft";

/** One way a workflow gets started. */
export interface WorkflowTrigger {
  kind: "issue" | "event" | "playbook" | "workflow";
  label: string;
  detail?: string;
  /** The event binding this trigger is, for editing it. */
  binding?: Binding;
}

export interface TriggerSources {
  /** Issue label kind to workflow, with "default" for an unlabelled issue. */
  routes: Record<string, string>;
  bindings: Binding[];
  playbooks: { id: string; yaml: string }[];
  definitions: { id: string; yaml: string }[];
}

type Mapping = Record<string, unknown>;

function isMapping(value: unknown): value is Mapping {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseQuietly(source: string): unknown {
  try {
    return parse(source);
  } catch {
    return null;
  }
}

/** Everything that starts the workflow named id. */
export function triggersFor(id: string, sources: TriggerSources): WorkflowTrigger[] {
  const triggers: WorkflowTrigger[] = [];
  for (const [kind, workflow] of Object.entries(sources.routes)) {
    if (workflow !== id) continue;
    triggers.push(
      kind === "default"
        ? { kind: "issue", label: "Unlabelled issues", detail: "anything no label routes elsewhere" }
        : { kind: "issue", label: `Issues labelled ${kind}` },
    );
  }
  for (const binding of sources.bindings) {
    if (binding.workflow !== id) continue;
    triggers.push({ kind: "event", label: binding.name || "Event binding", detail: binding.matcher?.source, binding });
  }
  for (const playbook of sources.playbooks) {
    const value = parseQuietly(playbook.yaml);
    if (!isMapping(value) || !Array.isArray(value.actions)) continue;
    if (!value.actions.some((action) => isMapping(action) && action.workflow === id)) continue;
    const trigger = isMapping(value.trigger) ? value.trigger : {};
    const labels = Array.isArray(trigger.labels) ? trigger.labels.join(", ") : "";
    triggers.push({ kind: "playbook", label: playbook.id, detail: [trigger.kind, labels].filter(Boolean).join(" · ") });
  }
  for (const definition of sources.definitions) {
    if (definition.id === id) continue;
    const how = startsVia(parseQuietly(definition.yaml), id);
    if (how) triggers.push({ kind: "workflow", label: definition.id, detail: how });
  }
  return triggers;
}

/** How a workflow's steps start the target: a call, a handoff (including one
 * whose target an earlier agent picks from a fixed list), or an approval. */
function startsVia(value: unknown, target: string): string {
  const steps = isMapping(value) && Array.isArray(value.steps) ? value.steps : [];
  const all = steps.flatMap((step) =>
    branchingOf(step)?.branches.flatMap(([, branch]) => branch) ?? [step],
  );
  for (const step of all) {
    if (!isMapping(step) || !isMapping(step.settings)) continue;
    const settings = step.settings;
    switch (step.type) {
      case "workflow.call":
        if (settings.workflow === target) return "calls it";
        break;
      case "workflow.handoff":
        if (settings.workflow === target || resultChoices(all, settings.workflow).includes(target)) return "hands off to it";
        break;
      case "human.approve":
        if (settings.then === target) return "runs it once approved";
        break;
    }
  }
  return "";
}

/** The fixed choices an agent's result field allows, for a reference like
 * {{ steps.classify.result.workflow }}. */
function resultChoices(steps: unknown[], reference: unknown): string[] {
  const match = typeof reference === "string" ? /\{\{\s*steps\.([\w-]+)\.result\.([\w-]+)\s*\}\}/.exec(reference) : null;
  if (!match) return [];
  const source = steps.find((step) => isMapping(step) && step.id === match[1]);
  const result = isMapping(source) && isMapping(source.settings) ? source.settings.result : undefined;
  const field = isMapping(result) && isMapping(result.properties) ? result.properties[match[2]] : undefined;
  return isMapping(field) && Array.isArray(field.enum) ? field.enum.map(String) : [];
}

/** The triggers of the workflow named id, from the routes and definitions the
 * control plane serves, the stored playbooks, and the event bindings. */
export function useWorkflowTriggers(id: Ref<string>) {
  const store = useControlPlaneStore();
  const bindings = ref<TriggerSources["bindings"]>([]);
  async function reload(): Promise<void> {
    const response = await api.bindings<{ bindings?: Binding[] }>().catch(() => null);
    bindings.value = response?.bindings ?? [];
  }
  onMounted(reload);
  const triggers = computed(() =>
    triggersFor(id.value, {
      routes: store.intakeRoutes(),
      bindings: bindings.value,
      playbooks: (store.stateFor("eda-playbooks").resource?.value as { playbooks?: TriggerSources["playbooks"] } | undefined)?.playbooks ?? [],
      definitions: (store.stateFor("workflow-definitions").resource?.value as { definitions?: TriggerSources["definitions"] } | undefined)?.definitions ?? [],
    }),
  );
  return { triggers, reload };
}
