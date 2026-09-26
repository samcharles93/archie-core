package org

import (
	"errors"
	"testing"
)

func TestRoleValidate(t *testing.T) {
	for _, r := range []Role{RoleOwner, RoleAdmin, RoleDeveloper, RoleViewer} {
		if err := r.Validate(); err != nil {
			t.Errorf("%q.Validate() = %v, want nil", r, err)
		}
	}
	for _, r := range []Role{"", "approver", "Owner"} {
		if err := r.Validate(); !errors.Is(err, ErrInvalidRole) {
			t.Errorf("%q.Validate() = %v, want ErrInvalidRole", r, err)
		}
	}
}

func TestMembershipValidate(t *testing.T) {
	tests := []struct {
		name string
		m    Membership
		want error
	}{
		{name: "org-wide", m: Membership{IdentityID: "id-1", OrgID: DefaultOrgID, Role: RoleOwner}},
		{name: "one workspace", m: Membership{IdentityID: "id-1", OrgID: DefaultOrgID, WorkspaceID: DefaultWorkspaceID, Role: RoleViewer}},
		{name: "no identity", m: Membership{OrgID: DefaultOrgID, Role: RoleOwner}, want: ErrInvalidMembership},
		{name: "no org", m: Membership{IdentityID: "id-1", Role: RoleOwner}, want: ErrInvalidMembership},
		{name: "bad role", m: Membership{IdentityID: "id-1", OrgID: DefaultOrgID, Role: "root"}, want: ErrInvalidRole},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.m.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestOrgValidate(t *testing.T) {
	tests := []struct {
		name string
		o    Org
		want error
	}{
		{name: "named", o: Org{ID: DefaultOrgID, Name: "Default"}},
		{name: "no id", o: Org{Name: "Default"}, want: ErrInvalidOrg},
		{name: "blank name", o: Org{ID: DefaultOrgID, Name: "  "}, want: ErrInvalidOrg},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.o.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAgentAssignmentValidate(t *testing.T) {
	valid := AgentAssignment{IdentityID: "id-1", OrgID: DefaultOrgID}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	for _, a := range []AgentAssignment{
		{},
		{OrgID: DefaultOrgID},
		{IdentityID: "id-1"},
	} {
		if err := a.Validate(); !errors.Is(err, ErrInvalidAssignment) {
			t.Errorf("%+v.Validate() = %v, want ErrInvalidAssignment", a, err)
		}
	}
}

func TestWorkspaceValidate(t *testing.T) {
	tests := []struct {
		name string
		w    Workspace
		want error
	}{
		{name: "named with environment", w: Workspace{ID: "w1", OrgID: DefaultOrgID, Name: "networking-prd", Environment: "production"}},
		{name: "environment optional", w: Workspace{ID: "w1", OrgID: DefaultOrgID, Name: "service-desk-dev"}},
		{name: "blank name", w: Workspace{ID: "w1", OrgID: DefaultOrgID, Name: "  "}, want: ErrInvalidWorkspace},
		{name: "no org", w: Workspace{ID: "w1", Name: "x"}, want: ErrInvalidWorkspace},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.w.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}
