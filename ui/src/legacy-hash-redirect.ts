// Rewrites a legacy `#/path` URL to `/path`.
const legacy = window.location.hash;

if (legacy.startsWith("#/")) {
  const path = legacy.slice(1);
  window.history.replaceState(null, "", path + window.location.search);
}
