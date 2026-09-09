// Which dashboard sections the serving process can actually back.
//
// From the UI cutover the dashboard is served by one composition: the
// extracted UI service, which holds two remote contracts (Gateway ChatContract
// and the State Store) and no daemon runtime handles. A section with nothing
// behind it still answers -- an empty list, a "disabled" marker, a 501 -- and
// the browser cannot tell that from a quiet deployment. GET /api/capabilities
// says which sections are backed here, and the nav drops the rest
// (archie-core-8cda.5.4).

// hiddenRoutes returns the paths whose section the server reported it cannot
// serve. An unreported section stays visible: a page that says "unavailable"
// is recoverable, a navigation entry that silently vanished is not, so an
// older server or a failed capabilities read shows everything.
export function hiddenRoutes(sections, routes) {
  if (!sections) return [];
  return routes.filter((r) => r.section && sections[r.section] === false).map((r) => r.path);
}
