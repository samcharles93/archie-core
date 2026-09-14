package cronstore

// Kind values for JobSpec.Kind: the discriminator that decides which delivery
// runner handles a job.
//
// They live here, beside the field that carries them, because they are part of
// the on-disk vocabulary — an operator writes one in jobs.json, the strict
// decoder reads it back, and a file written by a build that never heard of it
// must fail loudly rather than deliver the wrong way. Which runner serves a
// kind is not the store's business: the router in
// internal/infrastructure/crondelivery owns that mapping, which is why an
// unrecognised kind is left on disk rather than refused at Create.
const (
	// KindChat delivers the job's Payload.Text as a chat message through the
	// deployment's channel. It is the default: an empty Kind means KindChat,
	// so every job written before this field existed (schema version 1)
	// keeps delivering the way it did when it was created.
	KindChat = "chat"

	// KindWorkflow submits the job as a unit of work through the work-intake
	// path, so the existing task lifecycle owns scheduling, retries, gates
	// and PR opening.
	KindWorkflow = "workflow"
)
