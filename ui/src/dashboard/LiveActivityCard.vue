<script setup lang="ts">
import { useRouter } from "vue-router";

import { Badge } from "@/components/ui/badge";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import ActivityRow from "./ActivityRow.vue";
import { activity, streamStateKind, streamStateText } from "./state";

/** The last 50 events, newest first, with the stream's own state on the card. */

const router = useRouter();

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
        <Badge :variant="streamStateKind">{{ streamStateText }}</Badge>
      </CardAction>
    </CardHeader>
    <CardContent>
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
                  <EmptyDescription>Events appear here as Archie works.</EmptyDescription>
                </EmptyHeader>
              </Empty>
            </TableCell>
          </TableRow>
          <ActivityRow v-for="(event, i) in activity" v-else :key="i" :event="event" @open="openTask" />
        </TableBody>
      </Table>
    </CardContent>
  </Card>
</template>
