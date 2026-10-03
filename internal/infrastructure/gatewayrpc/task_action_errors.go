package gatewayrpc

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
)

// taskActionCodes maps task-action sentinels to the gRPC codes that carry
// them.
var taskActionCodes = []struct {
	code codes.Code
	err  error
}{
	{code: codes.NotFound, err: taskactions.ErrNotFound},
	{code: codes.FailedPrecondition, err: taskactions.ErrConflict},
	{code: codes.Aborted, err: storecontract.ErrStaleTransition},
	{code: codes.Unavailable, err: taskactions.ErrUnavailable},
}

// taskActionStatus classifies an action error for the wire, keeping the
// daemon's own wording as the status message.
func taskActionStatus(err error) error {
	if err == nil {
		return nil
	}
	for _, candidate := range taskActionCodes {
		if errors.Is(err, candidate.err) {
			return status.Error(candidate.code, err.Error())
		}
	}
	return err
}

// remoteTaskActionError restores a classified error on the caller's side,
// keeping the message the daemon produced.
type remoteTaskActionError struct {
	message  string
	sentinel error
}

func (e remoteTaskActionError) Error() string { return e.message }
func (e remoteTaskActionError) Unwrap() error { return e.sentinel }

// taskActionError is taskActionStatus's inverse. A code with no sentinel
// behind it (including a transport failure) is returned untouched, so an
// unclassified failure stays a server error rather than being dressed up as
// a known one.
func taskActionError(err error) error {
	if err == nil {
		return nil
	}
	reported, ok := status.FromError(err)
	if !ok {
		return err
	}
	for _, candidate := range taskActionCodes {
		if reported.Code() == candidate.code {
			return remoteTaskActionError{message: reported.Message(), sentinel: candidate.err}
		}
	}
	return err
}
