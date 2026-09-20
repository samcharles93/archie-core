package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/identity"
)

type controlPlaneClientStub struct {
	command *controlpb.CommandRequest
	query   *controlpb.Resource
	err     error
}

func (f *controlPlaneClientStub) Catalog(context.Context, *controlpb.CatalogRequest, ...grpc.CallOption) (*controlpb.CatalogResponse, error) {
	return &controlpb.CatalogResponse{}, f.err
}

func (f *controlPlaneClientStub) Query(context.Context, *controlpb.QueryRequest, ...grpc.CallOption) (*controlpb.QueryResponse, error) {
	return &controlpb.QueryResponse{Resource: f.query}, f.err
}

func (f *controlPlaneClientStub) Command(_ context.Context, request *controlpb.CommandRequest, _ ...grpc.CallOption) (*controlpb.CommandResponse, error) {
	f.command = request
	if f.err != nil {
		return nil, f.err
	}
	return &controlpb.CommandResponse{Resource: &controlpb.Resource{Kind: request.Kind, Version: 2, ValueJson: request.ValueJson}}, nil
}

func (f *controlPlaneClientStub) Watch(context.Context, *controlpb.WatchRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[controlpb.WatchResponse], error) {
	return nil, f.err
}

func TestControlPlaneCommandSuppliesTrustedAttribution(t *testing.T) {
	client := &controlPlaneClientStub{}
	server := &Server{ControlPlane: client}
	body := bytes.NewBufferString(`{"value":{"max_model_tool_steps":12},"expected_version":1,"actor":"browser"}`)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/control-plane/resources/workflow-execution-settings/commands/replace", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Archie-CSRF", "1")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if client.command.Actor != string(identity.SystemID) || client.command.Source != "archie-ui" {
		t.Fatalf("attribution = %q/%q, want the System identity from archie-ui", client.command.Actor, client.command.Source)
	}
	if !strings.HasPrefix(client.command.RequestId, "ui-") || client.command.ExpectedVersion != 1 {
		t.Fatalf("request = %+v", client.command)
	}
	var got struct {
		Resource controlPlaneResource `json:"resource"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Resource.Version != 2 || string(got.Resource.Value) != `{"max_model_tool_steps":12}` {
		t.Fatalf("resource = %+v", got.Resource)
	}
}

func TestControlPlaneConflictMapsToHTTPConflict(t *testing.T) {
	server := &Server{ControlPlane: &controlPlaneClientStub{err: status.Error(codes.Aborted, "version conflict")}}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/control-plane/resources/settings/commands/replace", strings.NewReader(`{"value":{},"expected_version":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Archie-CSRF", "1")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
}
