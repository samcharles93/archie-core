import { test } from "node:test";
import assert from "node:assert/strict";
import { matchRoute, navPath } from "../src/routing.jsx";

// The route table mirrors main.jsx's: a literal section and the parameterised
// detail page declared after it, which is the ordering that stops /tasks/:id
// from swallowing /tasks.
const routes = [
  { path: "/", label: "Dashboard" },
  { path: "/tasks", label: "Tasks", view: () => null },
  { path: "/tasks/:id", label: "Task run", view: () => null, nav: false, navPath: "/tasks" },
  { path: "/logs", label: "Logs", view: () => null },
];

test("a literal path matches its own route, not a parameterised one", () => {
  const match = matchRoute(routes, "/tasks");
  assert.equal(match.route.path, "/tasks");
  assert.deepEqual(match.params, {});
});

test("a path segment is captured as a route param", () => {
  const match = matchRoute(routes, "/tasks/42");
  assert.equal(match.route.path, "/tasks/:id");
  assert.deepEqual(match.params, { id: "42" });
});

test("the query string is not part of the path", () => {
  const match = matchRoute(routes, "/tasks/42?tab=changes&attempt=2");
  assert.equal(match.route.path, "/tasks/:id");
  assert.equal(match.params.id, "42");
});

test("a trailing slash names the same route", () => {
  assert.equal(matchRoute(routes, "/tasks/").route.path, "/tasks");
  assert.equal(matchRoute(routes, "/tasks/42/").params.id, "42");
});

// A prefix rule would send every unknown path under a section to that section's
// page, so a typo would render something plausible instead of the dashboard.
test("an unmatched path matches nothing rather than the nearest prefix", () => {
  assert.equal(matchRoute(routes, "/nope"), null);
  assert.equal(matchRoute(routes, "/tasks/42/extra"), null);
  assert.equal(matchRoute(routes, "/task"), null);
});

test("an encoded segment is decoded into the param", () => {
  assert.equal(matchRoute(routes, "/tasks/a%20b").params.id, "a b");
});

// W9: a detail route is not a nav entry, so it names the section it belongs to.
// Without this the Tasks item loses aria-current the moment the operator opens
// a run, and the whole nav reads as "nothing is current".
test("a parameterised route reports the section it keeps current", () => {
  assert.equal(navPath(routes[2]), "/tasks");
  assert.equal(navPath(routes[1]), "/tasks");
  assert.equal(navPath(undefined), "/");
});
