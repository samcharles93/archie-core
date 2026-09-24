<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import PageHeader from "@/base/PageHeader.vue";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import SkillCard, { type Skill } from "./SkillCard.vue";

const skills = ref<Skill[]>([]);
const search = ref("");
const loadError = ref<string | null>(null);

async function load() {
  try {
    const res = await api.skills<{ skills?: Skill[] }>();
    skills.value = res?.skills || [];
    loadError.value = null;
  } catch (err) {
    skills.value = [];
    loadError.value = String((err as Error).message || err);
  }
}

useLiveResource("skills", () => void load());
onMounted(load);

const filtered = computed(() => {
  const term = search.value.trim().toLowerCase();
  if (!term) return skills.value;
  return skills.value.filter((s) =>
    `${s.name} ${s.description}`.toLowerCase().includes(term),
  );
});
</script>

<template>
  <div>
    <PageHeader title="Skills" />

    <Card>
      <CardHeader>
        <CardTitle>Catalogue</CardTitle>
        <CardDescription
          >Project, shared, and user-global skills</CardDescription
        >
        <Input
          v-model="search"
          type="search"
          placeholder="Search skills…"
          aria-label="Search skills"
          class="max-w-xs"
        />
      </CardHeader>
      <CardContent>
        <div
          class="grid grid-cols-1 gap-4 min-[1080px]:grid-cols-[repeat(auto-fit,minmax(340px,1fr))]"
        >
          <Empty v-if="loadError">
            <EmptyHeader>
              <EmptyTitle>Cannot reach archied</EmptyTitle>
              <EmptyDescription>{{ loadError }}</EmptyDescription>
            </EmptyHeader>
          </Empty>
          <Empty v-else-if="!skills.length">
            <EmptyHeader>
              <EmptyTitle>No skills discovered yet</EmptyTitle>
              <EmptyDescription>
                Skills live as SKILL.md files under project, shared, or
                user-global .agents/skills/&lt;name&gt;/ directories.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
          <Empty v-else-if="!filtered.length">
            <EmptyHeader>
              <EmptyTitle>No skills match "{{ search }}"</EmptyTitle>
            </EmptyHeader>
          </Empty>
          <SkillCard
            v-for="skill in filtered"
            v-else
            :key="skill.name"
            :skill="skill"
          />
        </div>
      </CardContent>
    </Card>
  </div>
</template>
