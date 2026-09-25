<script setup lang="ts">
import { storeToRefs } from "pinia";
import { onMounted, ref } from "vue";

import ChatLauncher from "@/chat/ChatLauncher.vue";
import CommandPalette from "@/components/command-palette/CommandPalette.vue";
import Topbar from "@/components/topbar/Topbar.vue";
import SettingsShell from "@/settings/SettingsShell.vue";
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { TooltipProvider } from "@/components/ui/tooltip";
import { hidden, loadCapabilities } from "@/lib/capabilities";
import { createSessionRetry } from "@/lib/session-retry";
import { loadTaskMeta } from "@/lib/task-meta";
import { useLiveUpdatesStore } from "@/stores/live-updates";

const { authenticationRequired } = storeToRefs(useLiveUpdatesStore());

// Re-runs the reads that report their own 401s to the live-updates store. A
// session that came back clears the banner without a document reload; one that
// did not leaves it up.
const retrying = ref(false);
const retrySession = createSessionRetry([loadCapabilities, loadTaskMeta]);

async function retryAuthentication(): Promise<void> {
  retrying.value = true;
  try {
    await retrySession();
  } finally {
    retrying.value = false;
  }
}

onMounted(() => {
  // Asked for once, after the shell is up: the nav paints immediately and
  // loses the entries this process cannot back a moment later, rather than
  // holding the page behind a request. A failed read hides nothing.
  void loadCapabilities();

  // The lifecycle vocabulary -- status labels, pill severity, "needs you"
  // grouping, and the operator actions -- is served by /api/task-meta. Asked
  // for once, after the shell is up: the freeze-dried defaults paint
  // immediately and this replaces them, so a catalogue change on the server
  // reaches the browser without a UI release. loadTaskMeta never throws (a
  // failed read keeps the defaults), so it needs no catch here.
  void loadTaskMeta();
});
</script>

<template>
  <TooltipProvider :delay-duration="350">
    <!--
      One contained, rounded surface floating on the canvas `body` carries:
      framing the workspace gives the panels an edge to sit against, and lets
      the light behind read through rather than stopping at the first opaque
      card. The frame is a desktop affordance, so at the narrow breakpoint it
      goes edge to edge instead of insetting a rounded card on a phone.

      `backdrop-filter` makes this a containing block for `position: fixed`,
      so the chat launcher has to render outside this element.
    -->
    <div
      class="flex min-h-[calc(100vh-2rem)] flex-col overflow-hidden rounded-xl border border-[var(--hairline)] bg-[var(--workspace)] shadow-[var(--shadow-workspace)] backdrop-blur-[28px] backdrop-saturate-[1.2] max-lg:min-h-screen max-lg:rounded-none max-lg:border-0 max-lg:shadow-none"
    >
      <Topbar :hidden="hidden" />
      <main class="w-full flex-1 overflow-x-hidden p-8 max-lg:p-4">
        <Alert v-if="authenticationRequired" variant="destructive" class="mb-4">
          <AlertTitle>Dashboard authentication required</AlertTitle>
          <AlertDescription>
            Open the dashboard URL Archie logged at startup to establish a new
            authenticated session.
          </AlertDescription>
          <AlertAction>
            <Button
              variant="outline"
              size="sm"
              :disabled="retrying"
              @click="retryAuthentication"
            >
              {{ retrying ? "Retrying…" : "Retry" }}
            </Button>
          </AlertAction>
        </Alert>
        <!--
          Keyed on the path and its parameters but not the query: a query-only
          change is an entry state, so the page keeps the operator's filters,
          while two different :id values get fresh instances -- without that
          the second id diffs the first one in place and the previous task's
          open tab stays open on the next (W11).
        -->
        <RouterView v-slot="{ Component, route }">
          <SettingsShell v-if="route.meta.settings">
            <component :is="Component" :key="route.path" />
          </SettingsShell>
          <component :is="Component" v-else :key="route.path" />
        </RouterView>
      </main>
    </div>

    <!--
      Outside the shell on purpose: the frame above carries `backdrop-filter`,
      which makes it a containing block for `position: fixed`, so a launcher
      nested inside it would anchor to the shell rather than the viewport and
      ride off the bottom of a long page.
    -->
    <ChatLauncher />

    <!--
      Outside the shell for the same reason as the launcher above: the frame's
      backdrop-filter would otherwise become the containing block for the
      palette's fixed overlay. One instance for the whole app -- its global
      shortcut listener lives and dies with this mount.
    -->
    <CommandPalette :hidden="hidden" />
  </TooltipProvider>
</template>
