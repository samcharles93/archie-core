<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";
import { Plus, Trash2 } from "@lucide/vue";

import PageHeader from "@/base/PageHeader.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  NumberField,
  NumberFieldContent,
  NumberFieldDecrement,
  NumberFieldIncrement,
  NumberFieldInput,
} from "@/components/ui/number-field";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { SettingRow } from "@/components/ui/setting-row";
import { Switch } from "@/components/ui/switch";
import {
  TagsInput,
  TagsInputInput,
  TagsInputItem,
  TagsInputItemDelete,
  TagsInputItemText,
} from "@/components/ui/tags-input";
import { resourcesForPage, useControlPlaneStore } from "@/stores/control-plane";
import CommandListEditor from "./CommandListEditor.vue";
import DraftHint from "./DraftHint.vue";
import HistoryLink from "./HistoryLink.vue";

const KIND = "repository-policies";

interface Repo {
  owner: string;
  name: string;
  base: string;
  ecosystem: string;
  gate: string[][] | null;
  preflight: string[][] | null;
  protect: string[] | null;
  test_glob: string;
  persistent_storage: boolean;
  max_retries: number;
  allow_concurrent: boolean;
}

const store = useControlPlaneStore();
const { catalog, catalogError } = storeToRefs(store);
onMounted(store.load);

const resources = computed(() => resourcesForPage(catalog.value, "repositories"));
const draft = computed(() => store.drafts[KIND]);
const repos = computed(() => (draft.value?.value ?? []) as Repo[]);
const error = computed(() => store.stateFor(KIND).error);

const ecosystems = ["go", "python", "node", "rust", "custom"];
const newRepo = ref("");
function addRepo() {
  const [owner, name] = newRepo.value.trim().split("/");
  if (!draft.value || !owner || !name) return;
  draft.value.value ??= [];
  (draft.value.value as Repo[]).push({
    owner, name, base: "main", ecosystem: "go", gate: [], preflight: null, protect: null,
    test_glob: "", persistent_storage: false, max_retries: 0, allow_concurrent: false,
  });
  newRepo.value = "";
}
const protectOf = (repo: Repo) => repo.protect ?? [];
</script>

<template>
  <div>
    <PageHeader title="Repositories">
      <HistoryLink :kinds="resources.map((r) => r.kind)" />
    </PageHeader>

    <p v-if="catalogError || error" class="mb-4 text-sm text-danger" role="alert">{{ catalogError || error }}</p>

    <template v-if="draft">
      <section
        v-for="(repo, i) in repos"
        :key="i"
        class="mb-4 rounded-lg border border-border bg-card px-5 pt-4 pb-2"
        :aria-label="`${repo.owner}/${repo.name}`"
      >
        <header class="flex items-center gap-2">
          <h2 class="min-w-0 flex-1 truncate font-mono text-[15px] font-medium">{{ repo.owner }}/{{ repo.name }}</h2>
          <Button variant="ghost" size="icon" :aria-label="`Remove ${repo.owner}/${repo.name}`" @click="repos.splice(i, 1)"><Trash2 /></Button>
        </header>
        <SettingRow label="Base branch" :for="`repo-${i}-base`">
          <div class="flex flex-wrap items-center gap-3">
            <Input :id="`repo-${i}-base`" v-model="repo.base" class="max-w-56 font-mono" />
            <DraftHint :kind="KIND" :path="`${i}.base`" />
          </div>
        </SettingRow>
        <SettingRow label="Ecosystem">
          <div class="flex flex-wrap items-center gap-3">
            <Select v-model="repo.ecosystem">
              <SelectTrigger class="w-40 font-mono" :aria-label="`Ecosystem for ${repo.name}`"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="e in ecosystems" :key="e" :value="e">{{ e }}</SelectItem>
              </SelectContent>
            </Select>
            <DraftHint :kind="KIND" :path="`${i}.ecosystem`" />
          </div>
        </SettingRow>
        <SettingRow label="Quality gate" hint="Last command runs the tests.">
          <CommandListEditor v-model="repo.gate" label="Gate" empty-text="No gate." />
          <DraftHint :kind="KIND" :path="`${i}.gate`" />
        </SettingRow>
        <SettingRow label="Preflight" hint="Empty: ecosystem default.">
          <CommandListEditor v-model="repo.preflight" label="Preflight" empty-text="Ecosystem default." />
          <DraftHint :kind="KIND" :path="`${i}.preflight`" />
        </SettingRow>
        <SettingRow label="Test files" :for="`repo-${i}-glob`" hint="Empty: ecosystem default.">
          <div class="flex flex-wrap items-center gap-3">
            <Input :id="`repo-${i}-glob`" v-model="repo.test_glob" class="max-w-56 font-mono" placeholder="*_test.go" />
            <DraftHint :kind="KIND" :path="`${i}.test_glob`" />
          </div>
        </SettingRow>
        <SettingRow label="Protected paths" hint="Suffixes agents never write.">
          <div class="flex flex-wrap items-center gap-3">
            <TagsInput
              :model-value="protectOf(repo)"
              class="font-mono"
              :aria-label="`Protected paths for ${repo.name}`"
              @update:model-value="(v) => (repo.protect = v as string[])"
            >
              <TagsInputItem v-for="p in protectOf(repo)" :key="p" :value="p">
                <TagsInputItemText />
                <TagsInputItemDelete />
              </TagsInputItem>
              <TagsInputInput placeholder="_templ.go" />
            </TagsInput>
            <DraftHint :kind="KIND" :path="`${i}.protect`" />
          </div>
        </SettingRow>
        <SettingRow label="Max retries" hint="0 = scheduling policy value.">
          <div class="flex flex-wrap items-center gap-3">
            <NumberField v-model="repo.max_retries" :min="0" class="w-32">
              <NumberFieldContent>
                <NumberFieldDecrement />
                <NumberFieldInput class="font-mono" :aria-label="`Max retries for ${repo.name}`" />
                <NumberFieldIncrement />
              </NumberFieldContent>
            </NumberField>
            <DraftHint :kind="KIND" :path="`${i}.max_retries`" />
          </div>
        </SettingRow>
        <SettingRow label="Persistent storage" hint="Kept across tasks.">
          <div class="flex flex-wrap items-center gap-3">
            <Switch v-model="repo.persistent_storage" :aria-label="`Persistent storage for ${repo.name}`" />
            <DraftHint :kind="KIND" :path="`${i}.persistent_storage`" />
          </div>
        </SettingRow>
        <SettingRow
          label="Concurrent tasks"
          hint="Only if worktrees cannot collide."
          :tone="repo.allow_concurrent ? 'danger' : 'default'"
        >
          <div class="flex flex-wrap items-center gap-3">
            <Switch v-model="repo.allow_concurrent" :aria-label="`Concurrent tasks for ${repo.name}`" />
            <DraftHint :kind="KIND" :path="`${i}.allow_concurrent`" />
          </div>
        </SettingRow>
      </section>

      <div class="flex gap-2">
        <Input v-model="newRepo" class="max-w-72 font-mono" placeholder="owner/repository" aria-label="New repository" @keydown.enter="addRepo" />
        <Button variant="outline" size="sm" :disabled="!/^[^/\s]+\/[^/\s]+$/.test(newRepo.trim())" @click="addRepo">
          <Plus data-icon="inline-start" /> Add repository
        </Button>
      </div>
    </template>
  </div>
</template>
