package gateway

import (
	"context"
	"strings"
	"testing"
)

// recordingSlashDenials is the audit sink a refused command is reported to.
type recordingSlashDenials struct {
	denials []SlashDenial
}

func (r *recordingSlashDenials) RecordSlashDenial(_ context.Context, d SlashDenial) error {
	r.denials = append(r.denials, d)
	return nil
}

func TestSlashAccessPolicyAdminMayRunAdminCommand(t *testing.T) {
	policy := &SlashAccessPolicy{AllowAdminFrom: []string{"42"}}
	decision := policy.Decide("42", "/model")
	if !decision.Allowed || decision.Role != SlashRoleAdmin {
		t.Fatalf("Decide(admin, /model) = %+v, want allowed as admin", decision)
	}
}

func TestSlashAccessPolicyUserRefusedAdminCommand(t *testing.T) {
	policy := &SlashAccessPolicy{UserAllowedCommands: map[string][]string{"7": {}}}
	decision := policy.Decide("7", "/restart")
	if decision.Allowed {
		t.Fatalf("Decide(user, /restart) = %+v, want refused", decision)
	}
	if decision.Role != SlashRoleUser || decision.Reason == "" {
		t.Fatalf("Decide(user, /restart) = %+v, want a user refusal with a reason", decision)
	}
}

func TestSlashAccessPolicyUserMayRunUserCommand(t *testing.T) {
	policy := &SlashAccessPolicy{UserAllowedCommands: map[string][]string{"7": {}}}
	decision := policy.Decide("7", "/status")
	if !decision.Allowed || decision.Role != SlashRoleUser {
		t.Fatalf("Decide(user, /status) = %+v, want allowed as a user command", decision)
	}
}

func TestSlashAccessPolicyDelegatedAdminCommandIsAllowed(t *testing.T) {
	policy := &SlashAccessPolicy{UserAllowedCommands: map[string][]string{"7": {"/restart"}}}
	decision := policy.Decide("7", "/restart")
	if !decision.Allowed || decision.Role != SlashRoleUser {
		t.Fatalf("Decide(user, delegated /restart) = %+v, want allowed", decision)
	}
}

func TestSlashAccessPolicyUnknownSenderRefused(t *testing.T) {
	policy := &SlashAccessPolicy{AllowAdminFrom: []string{"42"}}
	decision := policy.Decide("99", "/status")
	if decision.Allowed {
		t.Fatalf("Decide(unknown, /status) = %+v, want refused", decision)
	}
	if decision.Role != SlashRoleUnknown || decision.Reason == "" {
		t.Fatalf("Decide(unknown, /status) = %+v, want an unknown-sender refusal", decision)
	}
}

func TestSlashAccessPolicyZeroValueDeniesEverything(t *testing.T) {
	var policy SlashAccessPolicy
	if decision := policy.Decide("anyone", "/status"); decision.Allowed {
		t.Fatalf("zero policy Decide() = %+v, want denied", decision)
	}
}

func TestSlashAccessPolicyAdminWinsOverUserGrant(t *testing.T) {
	policy := &SlashAccessPolicy{
		AllowAdminFrom:      []string{"7"},
		UserAllowedCommands: map[string][]string{"7": {}},
	}
	if role := policy.Role("7"); role != SlashRoleAdmin {
		t.Fatalf("Role(7) = %q, want admin to win over the user entry", role)
	}
}

func TestSlashAccessPolicyNormalisesGatewayMention(t *testing.T) {
	policy := &SlashAccessPolicy{AllowAdminFrom: []string{"42"}}
	if decision := policy.Decide("42", "/model@archie"); !decision.Allowed {
		t.Fatalf("Decide(admin, /model@archie) = %+v, want allowed", decision)
	}
}

func TestRouteRefusesAdminCommandForUnknownSender(t *testing.T) {
	models := &fakeModelManager{models: []string{"openai/gpt"}, activeModel: "openai/gpt"}
	router := NewRouter(nil, nil, "test")
	router.Models = models
	router.SlashAccess = &SlashAccessPolicy{AllowAdminFrom: []string{"admin-1"}}

	reply, err := router.Route(context.Background(), inbound("chan", "/model openai/gpt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "/model") || !strings.Contains(reply, "not permitted") {
		t.Fatalf("reply = %q, want a visible refusal naming the command", reply)
	}
	if models.activeModel != "openai/gpt" {
		t.Fatalf("activeModel = %q, the refused command executed", models.activeModel)
	}
}

func TestRouteAllowsAdminCommand(t *testing.T) {
	models := &fakeModelManager{models: []string{"openai/gpt"}, activeModel: "openai/gpt"}
	router := NewRouter(nil, nil, "test")
	router.Models = models
	router.SlashAccess = &SlashAccessPolicy{AllowAdminFrom: []string{"admin-1"}}

	in := inbound("chan", "/model openai/gpt")
	in.Message.SenderID = "admin-1"
	reply, err := router.Route(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "Active model set") {
		t.Fatalf("reply = %q, want the admin's command to run", reply)
	}
}

func TestRouteRefusesNonAdminAndRecordsDenial(t *testing.T) {
	restarts := 0
	router := NewRouter(nil, nil, "test")
	router.Restart = func(context.Context) error { restarts++; return nil }
	router.SlashAccess = &SlashAccessPolicy{UserAllowedCommands: map[string][]string{"user-7": {}}}
	denials := &recordingSlashDenials{}
	router.SlashDenials = denials

	in := inbound("chan", "/restart")
	in.Message.SenderID = "user-7"
	reply, err := router.Route(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if restarts != 0 {
		t.Fatalf("restarts = %d, the refused command executed", restarts)
	}
	if !strings.Contains(reply, "not permitted") {
		t.Fatalf("reply = %q, want a visible refusal", reply)
	}
	if len(denials.denials) != 1 {
		t.Fatalf("denials = %+v, want exactly one audit record", denials.denials)
	}
	got := denials.denials[0]
	if got.Sender != "user-7" || got.Command != "/restart" || got.Role != SlashRoleUser || got.Reason == "" {
		t.Fatalf("denial = %+v, want the refused sender, command, role and reason", got)
	}
}

func TestRouteRefusesUnknownSenderForUserCommand(t *testing.T) {
	router := NewRouter(&fakeStore{counts: map[string]int{}}, nil, "test")
	router.SlashAccess = &SlashAccessPolicy{UserAllowedCommands: map[string][]string{"user-7": {}}}

	reply, err := router.Route(context.Background(), inbound("chan", "/status"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reply, "Queue:") {
		t.Fatalf("reply = %q, the unknown sender's command executed", reply)
	}
	if !strings.Contains(reply, "not permitted") {
		t.Fatalf("reply = %q, want a visible refusal", reply)
	}
}

func TestRouteWithoutPolicyLeavesCommandsAvailable(t *testing.T) {
	router := NewRouter(&fakeStore{counts: map[string]int{}}, nil, "test")
	reply, err := router.Route(context.Background(), inbound("chan", "/status"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "Queue: idle") {
		t.Fatalf("reply = %q, want the command to run with no policy configured", reply)
	}
}
