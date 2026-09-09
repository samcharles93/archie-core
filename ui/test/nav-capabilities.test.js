import { test } from "node:test";
import assert from "node:assert/strict";
import { hiddenRoutes } from "../src/capabilities.jsx";

const routes = [
  { path: "/", label: "Dashboard" },
  { path: "/logs", label: "Logs", section: "logs" },
  { path: "/memory", label: "Memory", section: "memory" },
  { path: "/tasks", label: "Tasks" },
];

// The dashboard is served by two processes with different capabilities. A
// section the serving process cannot back renders as permanently empty, which
// reads as "nothing has happened" rather than "not served here".
test("a section the server cannot serve is hidden", () => {
  assert.deepEqual(hiddenRoutes({ logs: false, memory: true }, routes), ["/logs"]);
});

test("sections with no capability entry stay visible", () => {
  assert.deepEqual(hiddenRoutes({}, routes), []);
});

// Fail open: an older server, or a failed read, must not blank the nav.
test("no capabilities at all hides nothing", () => {
  assert.deepEqual(hiddenRoutes(undefined, routes), []);
  assert.deepEqual(hiddenRoutes(null, routes), []);
});

test("routes without a section are never hidden", () => {
  assert.deepEqual(hiddenRoutes({ logs: false }, routes), ["/logs"]);
});
