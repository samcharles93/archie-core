// Entity assembly: the engine's view of internal/domain/access's Principal,
// Resource and Context. The vocabulary (entity types, attributes) is the
// domain's; the shape is what the shipped schema declares.
package access

import (
	"time"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"

	"github.com/samcharles93/archie-core/internal/domain/access"
)

// buildRequest turns the assembled request into the engine's shape. The
// context record carries only the fields that were set: a policy testing a
// field the caller did not supply cannot match, which is the fail-closed
// reading of "context: the event's signature result, the address it came
// from, the time, and the run and step when the principal is an agent".
func buildRequest(p access.Principal, a access.Action, r access.Resource, c access.Context) types.Request {
	record := types.RecordMap{}
	if c.Signature != "" {
		record["signature"] = types.String(c.Signature)
	}
	if c.Addr != "" {
		if addr, err := types.ParseIPAddr(c.Addr); err == nil {
			record["addr"] = addr
		}
	}
	if c.Run != "" {
		record["run"] = types.String(c.Run)
	}
	if c.Step != "" {
		record["step"] = types.String(c.Step)
	}
	// The context always carries a time: the zero Time resolves to now.
	at := c.Time
	if at.IsZero() {
		at = time.Now().UTC()
	}
	record["time"] = types.NewDatetime(at)

	return types.Request{
		Principal: types.NewEntityUID(access.EntityTypeIdentity, types.String(p.IdentityID)),
		Action:    types.NewEntityUID(access.EntityTypeAction, types.String(string(a))),
		Resource:  types.NewEntityUID(access.EntityTypeObject, types.String(r.ID)),
		Context:   types.NewRecord(record),
	}
}

// buildEntities assembles the entity graph policies may walk: the principal
// (its org and effective role), the resource (its kind, org, workspace,
// owner and state), and the org and workspace parents the resource hangs
// from. A policy naming a parent the request does not supply simply does
// not match.
func buildEntities(p access.Principal, r access.Resource) cedar.EntityMap {
	resOrg := r.Org
	if resOrg == "" {
		resOrg = p.Org
	}
	entities := cedar.EntityMap{}
	orgUID := types.NewEntityUID(access.EntityTypeOrg, types.String(resOrg))
	entities[orgUID] = cedar.Entity{UID: orgUID}

	principalUID := types.NewEntityUID(access.EntityTypeIdentity, types.String(p.IdentityID))
	entities[principalUID] = cedar.Entity{
		UID: principalUID,
		// The principal's parent chain is its org; a policy may write
		// `principal in Archie::Org::"acme"`.
		Parents: cedar.NewEntityUIDSet(orgUID),
		Attributes: types.NewRecord(types.RecordMap{
			"org":  types.String(resOrg),
			"role": types.String(string(p.Role(r.Workspace))),
		}),
	}

	parents := []types.EntityUID{orgUID}
	if r.Workspace != "" {
		wsUID := types.NewEntityUID(access.EntityTypeWorkspace, types.String(r.Workspace))
		entities[wsUID] = cedar.Entity{
			UID:        wsUID,
			Parents:    cedar.NewEntityUIDSet(orgUID),
			Attributes: types.NewRecord(types.RecordMap{"org": types.String(resOrg)}),
		}
		parents = append(parents, wsUID)
	}

	resUID := types.NewEntityUID(access.EntityTypeObject, types.String(r.ID))
	attrs := types.RecordMap{
		"org":       types.String(resOrg),
		"workspace": types.String(string(r.Workspace)),
		"kind":      types.String(string(r.Kind)),
	}
	if r.Owner != "" {
		attrs["owner"] = types.String(string(r.Owner))
	}
	if r.State != "" {
		attrs["state"] = types.String(r.State)
	}
	entities[resUID] = cedar.Entity{
		UID:        resUID,
		Parents:    cedar.NewEntityUIDSet(parents...),
		Attributes: types.NewRecord(attrs),
	}
	return entities
}
