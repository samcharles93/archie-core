// Package webhookguard provides storage-agnostic mechanics for vetting
// inbound webhook-shaped HTTP requests before anything trusts them:
// signature verification, per-source rate limiting, and payload redaction.
//
// It is cross-cutting per docs/architecture/organisation.md: importable by
// any layer, and it imports none of domain, infrastructure, or app. It owns
// none of a caller's vocabulary -- not what a "source" is, not how a
// captured event is stored, not what a binding's approval state means. Those
// stay with the packages that own that state (see
// docs/prds/event-sources-and-reactions.md for why a generic Source
// interface was rejected).
package webhookguard
