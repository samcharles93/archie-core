package archiegateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/samcharles93/ai-sdk/chat"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/usage"
	"github.com/samcharles93/archie-core/internal/gateway"
)

const usageWriteTimeout = 5 * time.Second

// usageSink records the gateway's model calls. Chat has no org of its own
// yet, so its records belong to the system org. A nil sink records nothing.
type usageSink struct {
	store  usage.Recorder
	models gateway.ModelManager
	log    *slog.Logger
}

// record appends one call's usage. It never fails the call: the write is
// detached from the caller's cancellation and a failure is only logged.
func (u *usageSink) record(ctx context.Context, source usage.Source, ref string, used chat.Usage) {
	if u == nil || u.store == nil || ref == "" {
		return
	}
	alias := ""
	if u.models != nil && u.models.ActiveModel() == ref {
		alias = u.models.ActiveAlias()
	}
	record := usage.Record{
		Org: storecontract.DefaultOrgID, Source: source, Alias: alias,
		InputTokens: int64(used.PromptTokens), OutputTokens: int64(used.CompletionTokens),
		CachedTokens: int64(used.CachedTokens), At: time.Now().UTC(),
	}.WithRef(ref)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), usageWriteTimeout)
	defer cancel()
	if err := u.store.RecordUsage(ctx, record); err != nil && u.log != nil {
		u.log.Warn("record model usage failed", "source", source, "model", ref, "err", err)
	}
}
