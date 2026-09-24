<script setup lang="ts">
import { Plus, X } from "@lucide/vue";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { PayloadCondition, Rule } from "./event-types";

/**
 * A match rule's conditions, edited in place: header equalities, then payload
 * conditions. Every condition must hold for an event to match.
 */
const rule = defineModel<Rule>({ required: true });

function headers() {
  return (rule.value.headers ||= []);
}

function payload() {
  return (rule.value.payload ||= []);
}

function asOp(value: unknown): PayloadCondition["op"] {
  return value === "present" ? "present" : "equals";
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex flex-col gap-2">
      <Label>Headers</Label>
      <div v-for="(h, i) in rule.headers || []" :key="`h${i}`" class="flex items-center gap-2">
        <Input v-model="h.name" class="font-mono" aria-label="Header name" placeholder="X-GitHub-Event" />
        <span class="text-fg-subtle">=</span>
        <Input v-model="h.value" class="font-mono" aria-label="Header value" />
        <Button variant="ghost" size="icon-sm" aria-label="Remove header condition" @click="headers().splice(i, 1)">
          <X />
        </Button>
      </div>
      <Button variant="outline" size="sm" class="w-fit" @click="headers().push({ name: '', value: '' })">
        <Plus /> Header
      </Button>
    </div>

    <div class="flex flex-col gap-2">
      <Label>Payload</Label>
      <div v-for="(p, i) in rule.payload || []" :key="`p${i}`" class="flex items-center gap-2">
        <Input v-model="p.path" class="font-mono" aria-label="Payload path" placeholder="action" />
        <Select :model-value="p.op" @update:model-value="(value) => (p.op = asOp(value))">
          <SelectTrigger size="sm" class="w-28 shrink-0" aria-label="Condition">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="equals">equals</SelectItem>
              <SelectItem value="present">present</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
        <Input v-if="p.op === 'equals'" v-model="p.value" class="font-mono" aria-label="Payload value" />
        <span v-else class="w-full" />
        <Button variant="ghost" size="icon-sm" aria-label="Remove payload condition" @click="payload().splice(i, 1)">
          <X />
        </Button>
      </div>
      <Button variant="outline" size="sm" class="w-fit" @click="payload().push({ path: '', op: 'equals', value: '' })">
        <Plus /> Payload condition
      </Button>
    </div>
  </div>
</template>
