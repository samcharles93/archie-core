package archiegateway

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/skill"
)

// skillActivateTool is the registry name of the tool that loads a skill.
const skillActivateTool = "skill_activate"

// serveSkills registers skill_activate over the skills on disk and in the
// skills resource, then keeps it in step: an installed or withdrawn skill
// package reaches the next chat turn within one restamp interval, no restart.
// A skill on disk wins a name the resource also holds.
func (b *server) serveSkills(ctx context.Context) {
	var applied string
	b.refreshSkills(ctx, &applied)
	go func() {
		ticker := time.NewTicker(applystatus.RestampInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.refreshSkills(ctx, &applied)
			}
		}
	}()
}

func (b *server) refreshSkills(ctx context.Context, applied *string) {
	cfg, log := b.cfg, b.log
	catalog, err := skill.CatalogRoots(skill.DefaultRoots(cfg.WorkDir, cfg.SkillsDir)...)
	if err != nil {
		b.applyStatus.Report(ctx, controlplane.SkillsKind, 0, err)
		log.Warn("skill catalog load failed", "err", err)
		return
	}
	stored, version, err := b.storedSkills(ctx, catalog)
	if err != nil {
		b.applyStatus.Report(ctx, controlplane.SkillsKind, version, err)
		return
	}
	var applyErr error
	defer func() { b.applyStatus.Report(ctx, controlplane.SkillsKind, version, applyErr) }()
	catalog = append(catalog, stored...)
	b.setSkillList(catalog)
	signature := catalogSignature(catalog)
	if signature == *applied {
		return
	}
	b.toolReg.Unregister(skillActivateTool)
	if entry := skill.ActivateTool(cfg.WorkDir, catalog); entry != nil {
		if err := b.toolReg.Register(*entry); err != nil {
			applyErr = err
			log.Warn("skill_activate registration failed", "err", err)
			return
		}
		log.Info("skill catalog registered", "skills", len(catalog))
	}
	*applied = signature
}

// storedSkills reads the skills resource, leaving out any name already in
// have. An unreadable resource leaves the running catalog in place.
func (b *server) storedSkills(ctx context.Context, have []skill.CatalogEntry) ([]skill.CatalogEntry, int64, error) {
	var out []skill.CatalogEntry
	version, _, err := b.controlPlane.Query(ctx, controlplane.SkillsKind, func(value []byte) error {
		var collection controlplane.SkillCollection
		if err := json.Unmarshal(value, &collection); err != nil {
			return err
		}
		for _, held := range collection.Skills {
			entry, err := skill.EntryFromContent([]byte(held.Content))
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(have, func(e skill.CatalogEntry) bool { return e.Name == entry.Name }) {
				out = append(out, entry)
			}
		}
		return nil
	})
	if err != nil {
		b.log.Warn("skills resource unavailable", "err", err)
	}
	return out, version, err
}

func catalogSignature(catalog []skill.CatalogEntry) string {
	var b strings.Builder
	for _, e := range catalog {
		b.WriteString(e.Name + "\x00" + e.Description + "\x00" + e.Body + "\x00" + e.Dir + "\x00")
	}
	return b.String()
}
