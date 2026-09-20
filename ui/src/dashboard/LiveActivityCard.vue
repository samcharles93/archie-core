<script setup lang="ts">
import { storeToRefs } from "pinia";
import { useRouter } from "vue-router";

import { Badge } from "@/components/ui/badge";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLiveUpdatesStore } from "@/stores/live-updates";
import ActivityRow from "./ActivityRow.vue";

/** The last 50 events, newest first, with the stream's own state on the card. */

const router = useRouter();
const { activity, streamKind, streamState } = storeToRefs(useLiveUpdatesStore());

function openTask(taskID: number) {
  void router.push(`/tasks?task=${encodeURIComponent(taskID)}`);
}
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Live activity</CardTitle>
      <CardDescription>Last 50, newest first</CardDescription>
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
        <Table>
          <TableHeader>
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
            <ActivityRow v-for="(event, i) in activity" v-else :key="i" :event="event" @open="openTask" />
          </TableBody>
        </Table>
      </div>
    </CardContent>
  </Card>
</template>
