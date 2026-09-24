package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/identity"
)

type controlPlaneClientStub struct {
	command   *controlpb.CommandRequest
	query     *controlpb.Resource
	history   *controlpb.HistoryRequest
	revisions []*controlpb.Revision
	audit     *controlpb.AuditRequest
	entries   []*controlpb.AuditEntry
	watch     grpc.ServerStreamingClient[controlpb.WatchResponse]
	err       error
}

func (f *controlPlaneClientStub) Catalog(context.Context, *controlpb.CatalogRequest, ...grpc.CallOption) (*controlpb.CatalogResponse, error) {
	return &controlpb.CatalogResponse{}, f.err
}

func (f *controlPlaneClientStub) Query(context.Context, *controlpb.QueryRequest, ...grpc.CallOption) (*controlpb.QueryResponse, error) {
	return &controlpb.QueryResponse{Resource: f.query}, f.err
}

func (f *controlPlaneClientStub) History(_ context.Context, request *controlpb.HistoryRequest, _ ...grpc.CallOption) (*controlpb.HistoryResponse, error) {
	f.history = request
	return &controlpb.HistoryResponse{Revisions: f.revisions}, f.err
}

func (f *controlPlaneClientStub) Audit(_ context.Context, request *controlpb.AuditRequest, _ ...grpc.CallOption) (*controlpb.AuditResponse, error) {
	f.audit = request
	return &controlpb.AuditResponse{Entries: f.entries}, f.err
}

func (f *controlPlaneClientStub) Command(_ context.Context, request *controlpb.CommandRequest, _ ...grpc.CallOption) (*controlpb.CommandResponse, error) {
	f.command = request
	if f.err != nil {
		return nil, f.err
	}
	return &controlpb.CommandResponse{Resource: &controlpb.Resource{Kind: request.Kind, Version: 2, ValueJson: request.ValueJson}}, nil
}

func (f *controlPlaneClientStub) Watch(context.Context, *controlpb.WatchRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[controlpb.WatchResponse], error) {
	return f.watch, f.err
}

// idleWatch is a watch stream with nothing to deliver: Recv blocks until the
// request ends, as a real stream does while the resource is unchanged.
type idleWatch struct {
	grpc.ClientStream
	done <-chan struct{}
}

func (w idleWatch) Recv() (*controlpb.WatchResponse, error) {
	<-w.done
	return nil, context.Canceled
}

// TestControlPlaneWatchOpensBeforeTheFirstChange: a browser marks an event
// stream live when its headers arrive. Holding them until the first change
// leaves every settings card reading "Connecting" on an unchanged resource.
func TestControlPlaneWatchOpensBeforeTheFirstChange(t *testing.T) {
	server := httptest.NewServer((&Server{ControlPlane: &controlPlaneClientStub{watch: idleWatch{done: t.Context().Done()}}}).Handler())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/control-plane/watch/workflow-execution-settings", nil)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("watch did not answer before its first change: %v", err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
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

func TestControlPlaneHistoryRendersRevisionsForRestore(t *testing.T) {
	client := &controlPlaneClientStub{revisions: []*controlpb.Revision{
		{Version: 2, ValueJson: []byte(`{"max_model_tool_steps":40}`), Actor: string(identity.SystemID), Source: "archie-ui", RequestId: "ui-2"},
		{Version: 1, ValueJson: []byte(`{"max_model_tool_steps":25}`), Actor: "system:migration", Source: "legacy-config", RequestId: "import"},
	}}
	server := &Server{ControlPlane: client}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/control-plane/resources/workflow-execution-settings/history", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if client.history.GetKind() != "workflow-execution-settings" {
		t.Fatalf("history requested for %q", client.history.GetKind())
	}
	var got struct {
		Revisions []struct {
			Version int64           `json:"version"`
			Value   json.RawMessage `json:"value"`
			Actor   string          `json:"actor"`
			Source  string          `json:"source"`
		} `json:"revisions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Revisions) != 2 || got.Revisions[0].Version != 2 || got.Revisions[0].Actor != string(identity.SystemID) {
		t.Fatalf("revisions = %+v", got.Revisions)
	}
	// The value is the restore payload: it has to survive as JSON, not base64.
	if string(got.Revisions[1].Value) != `{"max_model_tool_steps":25}` {
		t.Fatalf("older value = %s, want the JSON a replace can send back", got.Revisions[1].Value)
	}
}

func TestControlPlaneHistoryWithoutAControlPlaneIsUnavailable(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/control-plane/resources/limits/history", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

// TestControlPlaneAuditFiltersByTableAndRecord: the page names its table and
// records; the endpoint forwards exactly those and returns each changed field
// with its JSON values intact.
func TestControlPlaneAuditFiltersByTableAndRecord(t *testing.T) {
	client := &controlPlaneClientStub{entries: []*controlpb.AuditEntry{{
		Id: 9, Table: "resources", RecordKey: "workflow-execution-settings", Field: "max_model_tool_steps",
		OldValueJson: []byte("90"), NewValueJson: []byte("120"), Version: 2, Actor: "system", Source: "archie-ui",
	}}}
	server := &Server{ControlPlane: client}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/control-plane/audit?table=resources&key=workflow-execution-settings&key=model-settings", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if client.audit.GetTable() != "resources" || strings.Join(client.audit.GetRecordKeys(), ",") != "workflow-execution-settings,model-settings" {
		t.Fatalf("forwarded %+v", client.audit)
	}
	var got struct {
		Entries []struct {
			Field    string          `json:"field"`
			OldValue json.RawMessage `json:"old_value"`
			NewValue json.RawMessage `json:"new_value"`
			Version  int64           `json:"version"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Field != "max_model_tool_steps" || string(got.Entries[0].OldValue) != "90" || string(got.Entries[0].NewValue) != "120" || got.Entries[0].Version != 2 {
		t.Fatalf("entries = %+v", got.Entries)
	}
}
