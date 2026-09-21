# Dashboard live state

**Status:** Approved

## Decision

The dashboard must not require passive **Refresh** buttons. One app-wide Pinia
store owns the existing `/events` EventSource and translates backend events
into resource revision counters. Pages load their authoritative HTTP projection
on entry and re-read it when the relevant revision changes. Event payloads are
invalidation signals, not substitute read models.

SSE remains the transport. Dashboard updates are server-to-client only, and the
existing stream already provides replay, reconnect, and persisted ordering; a
WebSocket would add a second transport without adding a capability.

The first slice covers resources for which the backend already emits a truthful
signal:

- task events invalidate the dashboard, task board, workflow statistics, and
  the matching task-run page;
- capture events invalidate the capture list;
- curator events invalidate curator status, and curator actions also invalidate
  the skill catalogue;
- update reports invalidate System status;
- opening a page always loads its resource, and reopening the SSE stream
  invalidates mounted projections after a connection loss.

Logs retain their dedicated SSE tail. Local mutations continue to re-read their
own resource after success. Configuration, channels, and catalogues that have no
backend change event load on route entry; future write contracts must emit an
owner-specific event rather than introduce polling or restore a button.

## Boundaries

The Pinia store owns browser connection state and invalidation only. It does not
become an alternate backend cache or copy domain state into a generic global
object. Existing feature modules continue to own their DTOs and API reads.

Operational commands are not refresh controls. Channel **Reload** remains an
explicit adapter lifecycle action. Authentication recovery is also not a data
refresh: a 401 moves the application into shared authentication state; reloading
the same URL cannot restore Archie's missing or rotated token and must not be
offered as a fix.

## Verification

- No passive page action renders the label **Refresh** or **Retry** for a read.
- Only one shared `/events` EventSource is opened by the application.
- Resource classification tests distinguish task, capture, curator, skill, and
  update invalidations.
- UI tests, type checking, generated asset freshness, and `task check` pass.
