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

const (
	controlPlaneActor  = "operator:web"
	controlPlaneSource = "archie-ui"
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
	requestID, err := newControlPlaneRequestID()
	if err != nil {
		http.Error(w, "cannot create request ID", http.StatusInternalServerError)
		return
	}
	response, err := s.ControlPlane.Command(r.Context(), &controlpb.CommandRequest{
		Kind: r.PathValue("kind"), Command: r.PathValue("command"), ValueJson: request.Value,
		ExpectedVersion: request.ExpectedVersion, Actor: controlPlaneActor,
		Source: controlPlaneSource, RequestId: requestID,
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
