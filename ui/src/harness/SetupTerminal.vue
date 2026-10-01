<script setup lang="ts">
/**
 * The setup terminal: an xterm.js surface over the WebSocket the webui route
 * upgrades to a Kit container's PTY
 * (docs/prds/external-agent-harness.md, "Setup terminal"). The browser cannot
 * set headers on a WebSocket, so the shared-token gate reads the same
 * HttpOnly cookie the SSE stream uses.
 *
 * The transport is raw bytes in both directions. Sizing the remote PTY is a
 * contract detail the daemon side will own with the session, so this
 * component fits the local surface but does not claim to resize the
 * container.
 */
import { onBeforeUnmount, onMounted, ref } from "vue";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";

import { setupTerminalURL } from "./harness";

const props = defineProps<{ profile: string }>();

const host = ref<HTMLDivElement | null>(null);
const status = ref<"connecting" | "open" | "closed" | "failed">("connecting");

let term: Terminal | null = null;
let socket: WebSocket | null = null;
let fit: FitAddon | null = null;
let observer: ResizeObserver | null = null;

function fitLocal(): void {
  try {
    fit?.fit();
  } catch {
    // xterm throws when the host has no measurable box yet; the next resize
    // (or open) retries. Fitting is cosmetic and must not fail the session.
  }
}

onMounted(() => {
  const mount = host.value;
  if (!mount) return;

  term = new Terminal({
    convertEol: true,
    cursorBlink: true,
    fontFamily: "'IBM Plex Mono', ui-monospace, monospace",
    fontSize: 13,
    theme: {
      background: "#0b0f14",
      foreground: "#e6edf3",
      cursor: "#e6edf3",
      selectionBackground: "#264f78",
    },
  });
  fit = new FitAddon();
  term.loadAddon(fit);
  term.open(mount);
  fitLocal();
  term.focus();

  const ws = new WebSocket(setupTerminalURL(props.profile, window.location));
  socket = ws;
  ws.binaryType = "arraybuffer";
  ws.onopen = () => {
    status.value = "open";
  };
  ws.onmessage = (event: MessageEvent) => {
    if (!term) return;
    if (typeof event.data === "string") {
      term.write(event.data);
    } else {
      term.write(new Uint8Array(event.data as ArrayBuffer));
    }
  };
  ws.onerror = () => {
    status.value = "failed";
  };
  ws.onclose = () => {
    status.value = "closed";
  };
  term.onData((data) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(data);
  });

  observer = new ResizeObserver(fitLocal);
  observer.observe(mount);
});

onBeforeUnmount(() => {
  observer?.disconnect();
  observer = null;
  socket?.close();
  socket = null;
  term?.dispose();
  term = null;
  fit = null;
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-2">
    <p
      v-if="status !== 'open'"
      class="text-sm text-muted-foreground"
      role="status"
    >
      <template v-if="status === 'connecting'">Connecting…</template>
      <template v-else-if="status === 'closed'">The terminal session ended.</template>
      <template v-else>Could not open the setup terminal.</template>
    </p>
    <div
      ref="host"
      class="min-h-0 flex-1 overflow-hidden rounded-md border border-border bg-[#0b0f14] p-2"
    />
  </div>
</template>
