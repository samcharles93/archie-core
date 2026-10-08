// Package logrpc encodes bounded service log snapshots over the existing contracts.
package logrpc

import (
	"encoding/json"

	pb "github.com/samcharles93/archie-core/internal/contracts/logging/v1"
	"github.com/samcharles93/archie-core/internal/logging"
)

func Query(r *pb.LogQuery) logging.Query {
	return logging.Query{Levels: r.GetLevels(), Component: r.GetComponent(), Contains: r.GetContains(), Limit: int(r.GetLimit())}
}

func Request(q logging.Query) *pb.LogQuery {
	return &pb.LogQuery{Levels: q.Levels, Component: q.Component, Contains: q.Contains, Limit: int32(q.Limit)}
}

func Encode(result logging.Result, err error) (*pb.LogSnapshot, error) {
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result.Entries)
	return &pb.LogSnapshot{EntriesJson: raw, Truncated: result.Truncated}, err
}

func Decode(result *pb.LogSnapshot, err error) (logging.Result, error) {
	if err != nil {
		return logging.Result{}, err
	}
	out := logging.Result{Truncated: result.Truncated}
	err = json.Unmarshal(result.EntriesJson, &out.Entries)
	return out, err
}
