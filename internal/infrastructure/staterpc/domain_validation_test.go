package staterpc

import (
	"context"
	"log/slog"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// The two recorders embed the store interface so only the methods under test
// need an implementation; a call to any other method panics rather than
// silently succeeding, which is what a test exercising one RPC wants.
type mappingRecorder struct {
	storecontract.MappingStore
	inserted, updated int
}

func (r *mappingRecorder) InsertMapping(context.Context, mapping.Mapping) (string, error) {
	r.inserted++
	return "mapping-1", nil
}

func (r *mappingRecorder) UpdateMapping(context.Context, mapping.Mapping) error {
	r.updated++
	return nil
}

type bindingRecorder struct {
	storecontract.BindingStore
	inserted, updated int
}

func (r *bindingRecorder) InsertBinding(context.Context, binding.Binding) (string, error) {
	r.inserted++
	return "binding-1", nil
}

func (r *bindingRecorder) UpdateBinding(context.Context, binding.Binding) error {
	r.updated++
	return nil
}

func validationOnlyServer(mappings storecontract.MappingStore, bindings storecontract.BindingStore) *server {
	return &server{deps: Deps{
		Mappings: mappings, Bindings: bindings,
		Log: slog.New(slog.DiscardHandler),
	}}
}

// TestDomainManagedRPCsApplyTheFeatureValidation pins that the domain API the
// control-plane catalog advertises for capture-mappings and capture-bindings
// enforces the feature's own rule. The rule is owned by
// internal/domain/mapping and internal/domain/binding and applied by the
// dashboard today; a stored path that does not apply it accepts a value the
// feature refuses, which is the two-validators disagreement this closes. The
// refused value must not reach the store.
func TestDomainManagedRPCsApplyTheFeatureValidation(t *testing.T) {
	t.Run("insert mapping", func(t *testing.T) {
		if err := (mapping.Mapping{}).Validate(); err == nil {
			t.Fatal("precondition: the mapping domain accepts an unnamed mapping")
		}
		recorder := &mappingRecorder{}
		_, err := validationOnlyServer(recorder, nil).InsertMapping(t.Context(), &pb.InsertMappingRequest{Mapping: &pb.Mapping{}})
		if err == nil {
			t.Fatal("InsertMapping accepted a mapping the mapping domain refuses")
		}
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Errorf("InsertMapping error code = %v, want %v (%v)", code, codes.InvalidArgument, err)
		}
		if recorder.inserted != 0 {
			t.Errorf("invalid mapping reached the store %d time(s)", recorder.inserted)
		}
	})

	t.Run("update mapping", func(t *testing.T) {
		recorder := &mappingRecorder{}
		_, err := validationOnlyServer(recorder, nil).UpdateMapping(t.Context(), &pb.UpdateMappingRequest{Mapping: &pb.Mapping{}})
		if err == nil {
			t.Fatal("UpdateMapping accepted a mapping the mapping domain refuses")
		}
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Errorf("UpdateMapping error code = %v, want %v (%v)", code, codes.InvalidArgument, err)
		}
		if recorder.updated != 0 {
			t.Errorf("invalid mapping reached the store %d time(s)", recorder.updated)
		}
	})

	t.Run("insert binding", func(t *testing.T) {
		if err := (binding.Binding{}).Validate(); err == nil {
			t.Fatal("precondition: the binding domain accepts an unnamed binding")
		}
		recorder := &bindingRecorder{}
		_, err := validationOnlyServer(nil, recorder).InsertBinding(t.Context(), &pb.InsertBindingRequest{Binding: &pb.Binding{}})
		if err == nil {
			t.Fatal("InsertBinding accepted a binding the binding domain refuses")
		}
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Errorf("InsertBinding error code = %v, want %v (%v)", code, codes.InvalidArgument, err)
		}
		if recorder.inserted != 0 {
			t.Errorf("invalid binding reached the store %d time(s)", recorder.inserted)
		}
	})

	t.Run("update binding", func(t *testing.T) {
		recorder := &bindingRecorder{}
		_, err := validationOnlyServer(nil, recorder).UpdateBinding(t.Context(), &pb.UpdateBindingRequest{Binding: &pb.Binding{}})
		if err == nil {
			t.Fatal("UpdateBinding accepted a binding the binding domain refuses")
		}
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Errorf("UpdateBinding error code = %v, want %v (%v)", code, codes.InvalidArgument, err)
		}
		if recorder.updated != 0 {
			t.Errorf("invalid binding reached the store %d time(s)", recorder.updated)
		}
	})
}
