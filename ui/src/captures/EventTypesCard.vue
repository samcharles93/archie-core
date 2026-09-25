<script setup lang="ts">
import {
  ClipboardPaste,
  Pencil,
  Tag,
  Trash2,
  TriangleAlert,
} from "@lucide/vue";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import EventTypeDialog from "./EventTypeDialog.vue";
import {
  confirmDelete,
  editEventType,
  eventTypes,
  eventTypesEnabled,
  eventTypesError,
  nameProposal,
  pasteExample,
  pendingDelete,
  proposals,
} from "./event-type-state";
import { ruleSummary } from "./event-types";

/**
 * Proposed event types (groups of unidentified captures sharing a structure)
 * and the named ones. Naming a proposal or pasting an example payload creates
 * a type; a later event matching it is identified as it on arrival.
 */
function fieldCount(schema: Record<string, string> | null | undefined): number {
  return schema ? Object.keys(schema).length : 0;
}
</script>

<template>
  <Card v-if="eventTypesEnabled">
    <CardHeader>
      <CardTitle>Event types</CardTitle>
      <CardAction>
        <Button variant="outline" size="sm" @click="pasteExample()"
          ><ClipboardPaste /> From payload</Button
        >
      </CardAction>
    </CardHeader>
    <CardContent class="flex flex-col gap-6">
      <Alert v-if="eventTypesError" variant="destructive">
        <TriangleAlert />
        <AlertTitle>Event types</AlertTitle>
        <AlertDescription>{{ eventTypesError }}</AlertDescription>
      </Alert>

      <Table v-if="proposals.length">
        <TableHeader>
          <TableRow>
            <TableHead>Proposed · source</TableHead>
            <TableHead class="w-20 text-right">Events</TableHead>
            <TableHead class="w-20 text-right">Fields</TableHead>
            <TableHead>Rule</TableHead>
            <TableHead class="w-24"
              ><span class="sr-only">Name</span></TableHead
            >
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="p in proposals" :key="p.source + p.key">
            <TableCell class="font-mono">{{
              p.source || "(unknown)"
            }}</TableCell>
            <TableCell class="text-right tabular-nums">{{ p.count }}</TableCell>
            <TableCell class="text-right tabular-nums">{{
              fieldCount(p.schema)
            }}</TableCell>
            <TableCell class="font-mono text-xs"
              ><span
                class="block max-w-96 truncate"
                :title="ruleSummary(p.rule)"
                >{{ ruleSummary(p.rule) }}</span
              ></TableCell
            >
            <TableCell class="text-right">
              <Button variant="outline" size="sm" @click="nameProposal(p)"
                ><Tag /> Name</Button
              >
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>

      <Table v-if="eventTypes.length">
        <TableHeader>
          <TableRow>
            <TableHead>Source</TableHead>
            <TableHead>Name</TableHead>
            <TableHead>Rule</TableHead>
            <TableHead class="w-20"
              ><span class="sr-only">Actions</span></TableHead
            >
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="t in eventTypes" :key="t.id">
            <TableCell class="font-mono">{{ t.source }}</TableCell>
            <TableCell>{{ t.name }}</TableCell>
            <TableCell class="font-mono text-xs"
              ><span
                class="block max-w-96 truncate"
                :title="ruleSummary(t.rule)"
                >{{ ruleSummary(t.rule) }}</span
              ></TableCell
            >
            <TableCell class="text-right whitespace-nowrap">
              <Button
                variant="ghost"
                size="icon-sm"
                :aria-label="`Edit ${t.name}`"
                @click="editEventType(t)"
                ><Pencil
              /></Button>
              <Button
                variant="ghost"
                size="icon-sm"
                :aria-label="`Delete ${t.name}`"
                @click="pendingDelete = t"
                ><Trash2
              /></Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>

      <div
        v-if="!proposals.length && !eventTypes.length"
        class="flex flex-col items-center py-4 text-center"
      >
        <p class="text-sm font-medium">No event types yet</p>
        <p class="mt-1 max-w-md text-sm text-fg-muted">
          Captures that share a source and payload shape are grouped into proposed types here. Name one to use it in a
          mapping, or start from a pasted payload.
        </p>
      </div>
    </CardContent>
  </Card>
  <EventTypeDialog />
  <AlertDialog
    :open="pendingDelete !== null"
    @update:open="
      (open) => {
        if (!open) pendingDelete = null;
      }
    "
  >
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>Delete this event type?</AlertDialogTitle>
        <AlertDialogDescription>
          Events matching "{{ pendingDelete?.name }}" will arrive unidentified
          and will not dispatch.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <AlertDialogAction variant="destructive" @click="confirmDelete"
          >Delete</AlertDialogAction
        >
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
