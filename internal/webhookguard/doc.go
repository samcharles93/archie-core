// Package webhookguard provides storage-agnostic mechanics for vetting
// inbound webhook-shaped HTTP requests before anything trusts them:
// signature verification, per-source rate limiting, and payload redaction.
//
// It owns none of a caller's vocabulary -- not what a "source" is, not how a
// captured event is stored, not what a binding's approval state means. Those
// stay with the packages that own that state.
package webhookguard
