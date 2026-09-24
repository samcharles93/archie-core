package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
)

type controlPlaneCommandRequest struct {
	Value           json.RawMessage `json:"value"`
	ExpectedVersion int64           `json:"expected_version"`
}

type controlPlaneResource struct {
	Kind      string          `json:"kind"`
	Version   int64           `json:"version"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
}

type controlPlaneResourceResponse struct {
	Resource controlPlaneResource `json:"resource"`
}

// controlPlaneRevision is one entry of a resource's audit trail. Value is the
// payload a restore sends back through the replace command, so it stays raw
// JSON rather than the base64 a proto bytes field would otherwise render as.
type controlPlaneRevision struct {
	Version   int64           `json:"version"`
	Value     json.RawMessage `json:"value"`
	Actor     string          `json:"actor"`
	Source    string          `json:"source"`
	RequestID string          `json:"request_id"`
	At        *time.Time      `json:"at,omitempty"`
}

type controlPlaneHistoryResponse struct {
	Revisions []controlPlaneRevision `json:"revisions"`
}

func (s *Server) handleControlPlaneCatalog(w http.ResponseWriter, r *http.Request) {
	if s.ControlPlane == nil {
		http.Error(w, "control plane unavailable", http.StatusServiceUnavailable)
		return
	}
	response, err := s.ControlPlane.Catalog(r.Context(), &controlpb.CatalogRequest{})
	if err != nil {
		writeControlPlaneError(w, err)
		return
	}
	writeJSON(w, response)
}

func (s *Server) handleControlPlaneQuery(w http.ResponseWriter, r *http.Request) {
	if s.ControlPlane == nil {
		http.Error(w, "control plane unavailable", http.StatusServiceUnavailable)
		return
	}
	response, err := s.ControlPlane.Query(r.Context(), &controlpb.QueryRequest{Kind: r.PathValue("kind")})
	if err != nil {
		writeControlPlaneError(w, err)
		return
	}
	writeJSON(w, controlPlaneResourceResponse{Resource: controlPlaneResourceView(response.Resource)})
}

func (s *Server) handleControlPlaneHistory(w http.ResponseWriter, r *http.Request) {
	if s.ControlPlane == nil {
		http.Error(w, "control plane unavailable", http.StatusServiceUnavailable)
		return
	}
	response, err := s.ControlPlane.History(r.Context(), &controlpb.HistoryRequest{Kind: r.PathValue("kind")})
	if err != nil {
		writeControlPlaneError(w, err)
		return
	}
	revisions := make([]controlPlaneRevision, 0, len(response.Revisions))
	for _, revision := range response.Revisions {
		view := controlPlaneRevision{
			Version: revision.Version, Value: revision.ValueJson, Actor: revision.Actor,
			Source: revision.Source, RequestID: revision.RequestId,
		}
		if revision.At != nil {
			at := revision.At.AsTime()
			view.At = &at
		}
		revisions = append(revisions, view)
	}
	writeJSON(w, controlPlaneHistoryResponse{Revisions: revisions})
}

func (s *Server) handleControlPlaneCommand(w http.ResponseWriter, r *http.Request) {
	if s.ControlPlane == nil {
		http.Error(w, "control plane unavailable", http.StatusServiceUnavailable)
		return
	}
	var request controlPlaneCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Value) == 0 {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	audit, err := webAudit()
	if err != nil {
		http.Error(w, "cannot create request ID", http.StatusInternalServerError)
		return
	}
	response, err := s.ControlPlane.Command(r.Context(), &controlpb.CommandRequest{
		Kind: r.PathValue("kind"), Command: r.PathValue("command"), ValueJson: request.Value,
		ExpectedVersion: request.ExpectedVersion, Actor: string(audit.ActorID),
		Source: audit.Source, RequestId: audit.RequestID,
	})
	if err != nil {
		writeControlPlaneError(w, err)
		return
	}
	writeJSON(w, controlPlaneResourceResponse{Resource: controlPlaneResourceView(response.Resource)})
}

func (s *Server) handleControlPlaneWatch(w http.ResponseWriter, r *http.Request) {
	if s.ControlPlane == nil {
		http.Error(w, "control plane unavailable", http.StatusServiceUnavailable)
		return
	}
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if err != nil && r.URL.Query().Get("after") != "" {
		http.Error(w, "invalid after version", http.StatusBadRequest)
		return
	}
	stream, err := s.ControlPlane.Watch(r.Context(), &controlpb.WatchRequest{Kind: r.PathValue("kind"), AfterVersion: after})
	if err != nil {
		writeControlPlaneError(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Headers go out now: the browser marks the stream live on them, and an
	// unchanged resource may send nothing for hours.
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	for {
		response, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) || r.Context().Err() != nil {
			return
		}
		if recvErr != nil {
			return
		}
		payload, marshalErr := json.Marshal(controlPlaneResourceResponse{Resource: controlPlaneResourceView(response.Resource)})
		if marshalErr != nil {
			return
		}
		if _, err = w.Write(append(append([]byte("data: "), payload...), '\n', '\n')); err != nil {
			return
		}
		flusher.Flush()
	}
}

func controlPlaneResourceView(resource *controlpb.Resource) controlPlaneResource {
	if resource == nil {
		return controlPlaneResource{}
	}
	view := controlPlaneResource{Kind: resource.Kind, Version: resource.Version, Value: resource.ValueJson}
	if resource.UpdatedAt != nil {
		updatedAt := resource.UpdatedAt.AsTime()
		view.UpdatedAt = &updatedAt
	}
	return view
}

func newControlPlaneRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "ui-" + hex.EncodeToString(value[:]), nil
}

func writeControlPlaneError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch status.Code(err) {
	case codes.InvalidArgument:
		code = http.StatusBadRequest
	case codes.NotFound:
		code = http.StatusNotFound
	case codes.Aborted:
		code = http.StatusConflict
	case codes.Unavailable, codes.DeadlineExceeded:
		code = http.StatusServiceUnavailable
	}
	message := status.Convert(err).Message()
	if message == "" {
		message = http.StatusText(code)
	}
	http.Error(w, message, code)
}
