// Hash-route matching.
//
// Every route is declared with a literal path in main.jsx and matched in full:
// `#/tasks` and `#/tasks/42` are different pages, and a prefix rule would send
// every path under a section to the same view, so a typo would render a page
// instead of the dashboard. A segment written `:name` captures exactly one path
// segment, which is how a task id rides in the URL (`#/tasks/42`) rather than
// only in the query string (`#/tasks?task=42` -- an existing deep link that
// keeps working).
//
// This lives in its own module because main.jsx renders on import and cannot be
// imported by a test; the matcher is the piece with decisions in it.

/**
 * matchRoute returns `{ route, params }` for the first route whose pattern
 * matches, or null. `params` is an object, empty for a literal route.
 */
export function matchRoute(routes, rawPath) {
  const path = normalisePath(rawPath);
  for (const route of routes) {
    const params = matchPattern(route.path, path);
    if (params) return { route, params };
  }
  return null;
}

/**
 * navPath is the navigation section a route belongs to. A parameterised route
 * is not a nav entry itself, so it names the section it was opened from -- the
 * per-task page keeps the Tasks item current instead of blanking the highlight.
 */
export function navPath(route) {
  return route?.navPath || route?.path || "/";
}

function normalisePath(rawPath) {
  const path = String(rawPath ?? "/").split("?", 1)[0].split("#", 1)[0];
  if (!path) return "/";
  // A trailing slash names the same route: #/tasks/ is #/tasks, and #/tasks/42/
  // is #/tasks/42.
  return path.length > 1 && path.endsWith("/") ? path.slice(0, -1) : path;
}

function matchPattern(pattern, path) {
  const want = String(pattern).split("/");
  const got = String(path).split("/");
  if (want.length !== got.length) return null;
  const params = {};
  for (let i = 0; i < want.length; i += 1) {
    const segment = want[i];
    if (segment.startsWith(":")) {
      if (!got[i]) return null;
      params[segment.slice(1)] = decodeURIComponent(got[i]);
      continue;
    }
    if (segment !== got[i]) return null;
  }
  return params;
}
