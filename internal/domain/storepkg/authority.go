package storepkg

import (
	"fmt"
	"strings"
)

// Covers reports whether accepted authority is an upper bound of the requested
// authority: every grant the request asks for is one the accepted record
// grants, per field, never across fields. The acceptance record and the
// update-widening check share this predicate.
func (a Authority) Covers(b Authority) bool {
	return listCovers(a.CredentialServices, b.CredentialServices) &&
		listCovers(a.EgressHosts, b.EgressHosts) &&
		listCovers(a.ForgePermissions, b.ForgePermissions) &&
		listCovers(a.Triggers, b.Triggers) &&
		listCovers(a.Tools, b.Tools) &&
		listCovers(a.Env, b.Env)
}

func listCovers(have, want []string) bool {
	granted := make(map[string]struct{}, len(have))
	for _, grant := range have {
		granted[grant] = struct{}{}
	}
	for _, grant := range want {
		if _, ok := granted[grant]; !ok {
			return false
		}
	}
	return true
}

// Validate checks authority grant values: forge permissions use the
// descriptor's vocabulary and every grant list rejects blank entries.
func (a Authority) Validate() error {
	for _, permission := range a.ForgePermissions {
		if !validForgePermission(permission) {
			return fmt.Errorf("invalid forge permission %q", permission)
		}
	}
	fields := []struct {
		name   string
		grants []string
	}{
		{name: "credential service", grants: a.CredentialServices},
		{name: "egress host", grants: a.EgressHosts},
		{name: "trigger", grants: a.Triggers},
		{name: "tool", grants: a.Tools},
		{name: "env var", grants: a.Env},
	}
	for _, field := range fields {
		for i, grant := range field.grants {
			if strings.TrimSpace(grant) == "" {
				return fmt.Errorf("%s %d: name is required", field.name, i)
			}
		}
	}
	return nil
}
