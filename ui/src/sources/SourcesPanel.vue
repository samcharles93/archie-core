<script setup lang="ts">
import { Check, Copy, KeyRound, Lock, LockOpen, Plus } from "@lucide/vue";
import { onMounted, ref } from "vue";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { useLiveResource } from "@/stores/live-updates";
import {
  signingKind,
  signingLabel,
  sourceURL,
  type Source,
} from "./source-signing";
import SourceSecret from "./SourceSecret.vue";
import { useSources } from "./use-sources";

/**
 * Capture sources: the endpoint each sender posts to and its signing setting
 * (docs/prds/event-automation.md "Sources"). Turning signing off is a request
 * that takes effect only once approved.
 */
const {
  sources,
  failure,
  revealed,
  busy,
  load,
  create,
  newSecret,
  requestUnsigned,
  requireSigning,
  approveUnsigned,
  refusals,
} = useSources();

const customPath = ref("");
const copied = ref<string | null>(null);

useLiveResource(null, () => void load());
onMounted(load);

async function copy(value: string): Promise<void> {
  await navigator.clipboard.writeText(value);
  copied.value = value;
  setTimeout(() => (copied.value = null), 1500);
}

async function submit(): Promise<void> {
  await create(customPath.value);
  if (!failure.value) customPath.value = "";
}

function url(source: Source): string {
  return sourceURL(window.location.origin, source.path);
}
</script>

<template>
  <section class="mb-6 flex flex-col gap-3">
    <form
      class="flex flex-wrap items-center justify-end gap-2"
      @submit.prevent="submit"
    >
      <Input
        v-model="customPath"
        class="w-64 font-mono"
        placeholder="Custom path (optional)"
        aria-label="Custom path"
      />
      <Button type="submit" variant="outline" :disabled="busy">
        <Plus data-icon="inline-start" />
        New source
      </Button>
    </form>

    <Alert v-if="failure" variant="destructive">
      <AlertTitle>That did not take effect</AlertTitle>
      <AlertDescription>{{ failure }}</AlertDescription>
    </Alert>

    <SourceSecret v-if="revealed" :source="revealed" @dismiss="revealed = null" />

    <Card v-if="sources && sources.length">
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Source</TableHead>
              <TableHead>Signing</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="source in sources" :key="source.path">
              <TableCell
                class="max-w-0 truncate font-mono text-fg-muted"
                :title="url(source)"
                >{{ source.path }}
                <div
                  v-if="refusals[source.path]"
                  class="text-xs font-sans text-destructive"
                  :title="refusals[source.path].addrs.join(', ')"
                >
                  {{ refusals[source.path].refused }} refused in 24h from
                  {{ refusals[source.path].addrs.slice(0, 3).join(", ") }}
                </div></TableCell
              >
              <TableCell>
                <Badge :variant="signingKind(source.signing)">{{
                  signingLabel(source.signing)
                }}</Badge>
              </TableCell>
              <TableCell>
                <div class="flex items-center justify-end gap-1">
                  <Tooltip>
                    <TooltipTrigger as-child>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label="Copy URL"
                        @click="copy(url(source))"
                      >
                        <Check v-if="copied === url(source)" />
                        <Copy v-else />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent>Copy URL</TooltipContent>
                  </Tooltip>
                  <Tooltip>
                    <TooltipTrigger as-child>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label="New secret"
                        :disabled="busy"
                        @click="newSecret(source)"
                      >
                        <KeyRound />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent>New secret</TooltipContent>
                  </Tooltip>
                  <Button
                    v-if="source.signing === 'signed'"
                    variant="outline"
                    size="sm"
                    :disabled="busy"
                    @click="requestUnsigned(source)"
                  >
                    <LockOpen data-icon="inline-start" />
                    Turn off signing
                  </Button>
                  <Button
                    v-if="source.signing === 'unsigned_pending_approval'"
                    size="sm"
                    :disabled="busy"
                    @click="approveUnsigned(source)"
                  >
                    <Check data-icon="inline-start" />
                    Approve unsigned
                  </Button>
                  <Button
                    v-if="source.signing !== 'signed'"
                    variant="outline"
                    size="sm"
                    :disabled="busy"
                    @click="requireSigning(source)"
                  >
                    <Lock data-icon="inline-start" />
                    Require signing
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  </section>
</template>
