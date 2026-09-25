<script setup lang="ts">
import { Inbox, TriangleAlert } from "@lucide/vue";
import { ref } from "vue";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import CaptureRow from "./CaptureRow.vue";
import { captures, enabled, error, loading } from "./state";

/**
 * The captures, or the one thing that stands in for them: a read that failed,
 * a deployment with no capture storage, a first read still in flight, or a
 * window with nothing in it yet.
 */

// The receiver's path, held as a string rather than typed into the template:
// its source segment is angle-bracketed, which a template parser reads as a
// tag.
const endpoint = "/webhooks/capture/<source>";
const copied = ref(false);
async function copyEndpoint() {
  await navigator.clipboard.writeText(`${location.origin}${endpoint}`);
  copied.value = true;
  setTimeout(() => (copied.value = false), 1500);
}
</script>

<template>
  <!-- A failed read is about this page's request, not about capture, so it says
       what the server said rather than borrowing an empty state. -->
  <Alert v-if="error" variant="destructive">
    <TriangleAlert />
    <AlertTitle>Cannot reach archied</AlertTitle>
    <AlertDescription>{{ error }}</AlertDescription>
  </Alert>

  <Empty v-else-if="!enabled">
    <EmptyHeader>
      <EmptyMedia variant="icon"><Inbox /></EmptyMedia>
      <EmptyTitle>Capture is not configured</EmptyTitle>
    </EmptyHeader>
  </Empty>

  <div v-else-if="loading" class="flex flex-col gap-2">
    <Skeleton v-for="row in 5" :key="row" class="h-9 w-full" />
  </div>

  <Empty v-else-if="!captures.length">
    <EmptyHeader>
      <EmptyMedia variant="icon"><Inbox /></EmptyMedia>
      <EmptyTitle>No captures yet</EmptyTitle>
      <EmptyDescription>Point a webhook at this address and it shows up here.</EmptyDescription>
    </EmptyHeader>
    <div class="flex items-center gap-2 rounded-md border border-border bg-background py-1 pr-1 pl-3">
      <code class="font-mono text-xs">{{ endpoint }}</code>
      <Button variant="ghost" size="sm" @click="copyEndpoint">{{ copied ? "Copied" : "Copy" }}</Button>
    </div>
  </Empty>

  <Table v-else>
    <TableHeader>
      <TableRow>
        <TableHead>Source</TableHead>
        <TableHead>Received</TableHead>
        <TableHead>Event type</TableHead>
        <TableHead>Content type</TableHead>
        <TableHead class="w-10"><span class="sr-only">Payload</span></TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <CaptureRow
        v-for="capture in captures"
        :key="capture.id"
        :capture="capture"
      />
    </TableBody>
  </Table>
</template>
