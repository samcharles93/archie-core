<script setup lang="ts">
import { storeToRefs } from "pinia";
import { computed, reactive } from "vue";
import { useRouter } from "vue-router";

import { ChevronDown, ChevronRight } from "@lucide/vue";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ago } from "@/lib/format";
import { useLiveUpdatesStore } from "@/stores/live-updates";
import ActivityRow from "./ActivityRow.vue";
import { activityDetail } from "./activity-detail";
import { groupActivity, type ActivityGroup } from "./activity-group";

/**
 * The last 50 events, newest first, with the stream's own state on the card.
 * Runs of consecutive same-kind same-task events (the daemon's background
 * telemetry) collapse into one group row carrying an ×N badge; expanding one
 * reveals its events. The grouping lives in activity-group.ts and is tested
 * there; this card only renders it.
 */

const router = useRouter();
const { activity, streamKind, streamState } = storeToRefs(
  useLiveUpdatesStore(),
);

// Expansion is keyed on label:taskID, so a live run stays open as its events
// arrive, and a collapsed group re-collapses only when the run breaks.
const expanded = reactive(new Set<string>());
const groups = computed(() => groupActivity(activity.value));

function toggle(key: string) {
  if (expanded.has(key)) expanded.delete(key);
  else expanded.add(key);
}

function isOpen(group: ActivityGroup): boolean {
  return expanded.has(group.key);
}

// The collapsed group row shows what its newest event would have shown, so
// expanding and collapsing moves no information.
function groupDetail(group: ActivityGroup) {
  return activityDetail(group.representative);
}

function groupWhen(group: ActivityGroup) {
  return group.representative.at ? ago(group.representative.at) : "\u2014";
}

function openTask(taskID: number) {
  void router.push(`/tasks?task=${encodeURIComponent(taskID)}`);
}
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Live activity</CardTitle>
      <CardAction>
        <Badge :variant="streamKind">{{ streamState }}</Badge>
      </CardAction>
    </CardHeader>
    <CardContent>
      <!--
        role="region" names the table for landmark navigation; aria-live is
        polite, not assertive, because rows arrive constantly while the stream
        is live and an assertive region would talk over the operator. The live
        region is the table's wrapper, so new rows are announced as they
        appear without a second hidden summary to keep in sync.
      -->
      <div role="region" aria-label="Live activity" aria-live="polite">
        <!--
          The feed's height is bounded to the card, not the page: group rows
          multiply with the number of interleaved tasks, and an unbounded
          table made the dashboard ~4,700px tall on a live instance. The
          newest events stay visible at the top; the rest scroll inside the
          card. The header stays put via sticky so the columns keep their
          labels while scrolling.
        -->
        <div class="max-h-[28rem] overflow-y-auto">
          <Table>
            <TableHeader class="sticky top-0 z-10 bg-card">
              <TableRow>
                <TableHead>Event</TableHead>
                <TableHead>Task</TableHead>
                <TableHead>Detail</TableHead>
                <TableHead>When</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-if="!activity.length">
                <TableCell colspan="4">
                  <Empty>
                    <EmptyHeader>
                      <EmptyTitle>Waiting for activity</EmptyTitle>
                    </EmptyHeader>
                  </Empty>
                </TableCell>
              </TableRow>
              <template v-if="activity.length">
                <template v-for="group in groups" :key="group.key">
                  <!-- A singleton is exactly the row the raw feed would show. -->
                  <ActivityRow
                    v-if="group.count === 1"
                    :event="group.representative"
                    @open="openTask"
                  />
                  <template v-else>
                    <!--
                    The group row keeps ActivityRow's link contract: when its
                    events resolve to a task, the row itself opens the task,
                    with the same focusable role="link" behaviour. The
                    disclosure button is a stop-propagated control inside the
                    first cell, so it never fights the row-level link.
                  -->
                    <TableRow
                      :class="group.taskID > 0 ? 'cursor-pointer' : ''"
                      :role="group.taskID > 0 ? 'link' : undefined"
                      :tabindex="group.taskID > 0 ? 0 : undefined"
                      :title="
                        group.taskID > 0 ? 'Open task details' : undefined
                      "
                      @click="group.taskID > 0 && openTask(group.taskID)"
                      @keydown.enter.prevent="
                        group.taskID > 0 && openTask(group.taskID)
                      "
                      @keydown.space.prevent="
                        group.taskID > 0 && openTask(group.taskID)
                      "
                    >
                      <TableCell class="font-medium">
                        <button
                          type="button"
                          class="inline-flex items-center gap-1 rounded hover:opacity-80"
                          :aria-expanded="isOpen(group)"
                          :aria-label="`${isOpen(group) ? 'Hide' : 'Show'} ${group.count} ${group.label} events`"
                          @click.stop="toggle(group.key)"
                        >
                          <ChevronDown
                            v-if="isOpen(group)"
                            class="size-3.5 shrink-0 text-fg-subtle"
                          />
                          <ChevronRight
                            v-else
                            class="size-3.5 shrink-0 text-fg-subtle"
                          />
                          {{ group.label }}
                          <Badge variant="idle">×{{ group.count }}</Badge>
                        </button>
                      </TableCell>
                      <TableCell class="font-mono">{{
                        group.taskID > 0 ? `#${group.taskID}` : "—"
                      }}</TableCell>
                      <TableCell class="w-[55%] max-w-0">
                        <span
                          class="block truncate"
                          :title="
                            groupDetail(group).truncated
                              ? groupDetail(group).full
                              : undefined
                          "
                          >{{ groupDetail(group).text }}</span
                        >
                      </TableCell>
                      <TableCell>{{ groupWhen(group) }}</TableCell>
                    </TableRow>
                    <template v-if="isOpen(group)">
                      <ActivityRow
                        v-for="(event, i) in group.events"
                        :key="`${group.key}:${i}`"
                        :event="event"
                        @open="openTask"
                      />
                    </template>
                  </template>
                </template>
              </template>
            </TableBody>
          </Table>
        </div>
      </div>
    </CardContent>
  </Card>
</template>
