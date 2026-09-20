<script setup lang="ts">
import { computed } from "vue";

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { fileStatusLabel } from "./changed-files";
import type { Capture } from "./task-run";

/**
 * One capture's file entries. The counts beside a path are the diffstat the
 * daemon recorded at the producer, not a reading of the file on disk now.
 */
const props = defineProps<{ capture: Capture }>();

const files = computed(() => props.capture.files || []);
const total = computed(() => Number(props.capture.totals?.files) || files.value.length);
</script>

<template>
  <p v-if="!files.length" class="py-3 text-xs text-fg-muted">
    This capture recorded no file entries{{ total ? `, though its totals cover ${total} file${total === 1 ? "" : "s"}` : ""
    }}.
  </p>
  <template v-else>
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Path</TableHead>
          <TableHead>Change</TableHead>
          <TableHead class="text-right">Added</TableHead>
          <TableHead class="text-right">Deleted</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(file, i) in files" :key="`${file.path}:${i}`">
          <TableCell class="font-mono break-all whitespace-normal">
            <span v-if="file.old_path" class="text-fg-muted">{{ file.old_path }} → </span>{{ file.path }}
          </TableCell>
          <TableCell>
            {{ fileStatusLabel(file.status) }}
            <!-- binary means "no textual hunks were recorded", not "this is a
                 binary file on disk": the counts beside it are still real. -->
            <span v-if="file.binary" class="block text-xs text-fg-muted">no textual hunks</span>
          </TableCell>
          <TableCell class="text-right font-mono whitespace-nowrap">
            {{ file.additions ? `+${file.additions}` : "—" }}
          </TableCell>
          <TableCell class="text-right font-mono whitespace-nowrap">
            {{ file.deletions ? `−${file.deletions}` : "—" }}
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="capture.truncated" class="mt-2 text-xs text-fg-muted">
      Showing the first {{ files.length }} of {{ total }} files. The totals above cover every file in this capture.
    </p>
  </template>
</template>
