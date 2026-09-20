<script setup lang="ts">
import { computed, ref } from "vue";

import { api } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { parseEdit, valueText } from "./config-field";
import ConfigControl from "./ConfigControl.vue";
import { errorText, loadConfig } from "./state";
import type { ConfigField } from "./types";

/**
 * One row of a key/value list: the label, the value the process is running
 * with, and -- when the field is editable and this process holds the write
 * path -- the edit and reset controls.
 *
 * A locked field shows its value and the reason instead of a control: those
 * keys are the daemon's own bootstrap inputs, and changing one from here could
 * break the next boot. A process serving a published snapshot has no write
 * path at all, so it renders every editable field as a value rather than a
 * control that would answer 503 (archie-core-ymut).
 *
 * Every row is a subgrid of the list's three tracks, so the columns line up
 * across rows; see ConfigList.
 */
const props = defineProps<{
  label: string;
  value?: unknown;
  /** The descriptor that makes the row editable. Absent for a value-only row,
   * which is what the provenance list uses. */
  field?: ConfigField;
  /** Whether the serving process can apply changes at all. */
  editable?: boolean;
}>();

const text = computed(() => valueText(props.value));

const editing = ref(false);
const draft = ref("");
const error = ref<string | null>(null);
const saving = ref(false);
const resetting = ref(false);

const lockedReason = computed(() => props.field?.locked_reason ?? "");
const overridden = computed(() => Boolean(props.field?.overridden));
const canEdit = computed(() => Boolean(props.field?.editable) && props.editable !== false && !lockedReason.value);

function startEdit(): void {
  if (!props.field) return;
  draft.value = String(props.field.value ?? "");
  error.value = null;
  editing.value = true;
}

async function save(): Promise<void> {
  const target = props.field;
  if (!target) return;
  const parsed = parseEdit(target.type, draft.value);
  if (parsed.error) {
    error.value = parsed.error;
    return;
  }
  saving.value = true;
  error.value = null;
  try {
    await api.configUpdate({ [target.key]: parsed.value });
    editing.value = false;
    await loadConfig();
  } catch (err) {
    error.value = errorText(err);
  } finally {
    saving.value = false;
  }
}

async function reset(): Promise<void> {
  const target = props.field;
  if (!target) return;
  resetting.value = true;
  error.value = null;
  try {
    await api.configReset(target.key);
    await loadConfig();
  } catch (err) {
    error.value = errorText(err);
  } finally {
    resetting.value = false;
  }
}
</script>

<template>
  <div
    class="grid grid-cols-1 items-center gap-x-4 gap-y-1 border-t border-border py-2 first:border-t-0 hover:bg-muted/40 min-[701px]:grid-cols-[minmax(8rem,16rem)_minmax(0,1fr)_auto] min-[701px]:supports-[grid-template-columns:subgrid]:col-span-full min-[701px]:supports-[grid-template-columns:subgrid]:grid-cols-subgrid"
  >
    <!-- The field's own description is the row's tooltip: the line that
         explains a setting is the one place that explanation fits without
         pushing every row apart. -->
    <Tooltip v-if="field?.description">
      <TooltipTrigger as-child>
        <!--
          tabindex="0" is the keyboard path to the description: reka wires the
          trigger's focus/blur to the tooltip's open state, so Tab reaches the
          label and holding focus keeps the description on screen. The
          aria-label carries the label and the description together, because
          the tooltip content itself is out of the accessibility tree when the
          trigger is not hovered or focused. No visual change -- the focus
          floor in style.css draws the only new affordance, and only on
          keyboard focus.
        -->
        <span tabindex="0" :aria-label="`${label}: ${field.description}`" class="text-sm text-fg-muted">{{ label }}</span>
      </TooltipTrigger>
      <TooltipContent :side-offset="8" class="max-w-80">{{ field.description }}</TooltipContent>
    </Tooltip>
    <span v-else class="text-sm text-fg-muted">{{ label }}</span>

    <template v-if="editing && field">
      <ConfigControl v-model="draft" :type="field.type" :options="field.options" :disabled="saving" @save="save" @cancel="editing = false" />
      <div class="flex items-center gap-2 justify-self-end">
        <Button size="sm" :disabled="saving" @click="save">Save</Button>
        <Button variant="outline" size="sm" :disabled="saving" @click="editing = false">Cancel</Button>
      </div>
    </template>
    <template v-else>
      <!-- min-w-0 is load-bearing: a grid item refuses to shrink below its
           content by default, which is what pushed long paths into the action
           column and truncated them against the buttons. -->
      <slot name="value">
        <span
          :class="cn('min-w-0 break-words font-mono text-sm min-[701px]:truncate', text === '—' ? 'text-fg-subtle' : 'text-foreground', lockedReason && 'text-fg-muted')"
          :title="text"
        >{{ text }}</span>
      </slot>
      <!-- The controls sit in the row rather than appearing on hover: one that
           only exists on hover is invisible to touch and to anyone scanning
           for what is editable. -->
      <div v-if="canEdit" class="flex items-center gap-2 justify-self-end">
        <Badge v-if="overridden" variant="warn" title="Set from the dashboard; this shadows the value in the config file">overridden</Badge>
        <Button variant="ghost" size="sm" :title="`Edit ${label}`" @click="startEdit">Edit</Button>
        <Button
          v-if="overridden"
          variant="ghost"
          size="sm"
          title="Discard the dashboard value and fall back to the config file"
          :disabled="resetting"
          @click="reset"
        >
          Reset
        </Button>
      </div>
    </template>

    <span v-if="lockedReason" class="col-span-full text-xs text-fg-subtle min-[701px]:col-span-2 min-[701px]:col-start-2">{{ lockedReason }}</span>
    <span v-if="error" class="col-span-full text-xs text-danger min-[701px]:col-span-2 min-[701px]:col-start-2" role="alert">{{ error }}</span>
  </div>
</template>
