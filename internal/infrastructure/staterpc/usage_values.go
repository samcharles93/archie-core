package staterpc

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/usage"
)

func usageProto(r usage.Record) *pb.UsageRecord {
	return &pb.UsageRecord{
		Source: string(r.Source), TaskId: r.TaskID, Attempt: int64(r.Attempt),
		Workflow: r.Workflow, Step: r.Step, Alias: r.Alias, Provider: r.Provider, Model: r.Model,
		InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CachedTokens: r.CachedTokens,
		At: timestamppb.New(r.At),
	}
}

func usageValue(r *pb.UsageRecord) usage.Record {
	return usage.Record{
		Source: usage.Source(r.GetSource()), TaskID: r.GetTaskId(), Attempt: int(r.GetAttempt()),
		Workflow: r.GetWorkflow(), Step: r.GetStep(), Alias: r.GetAlias(), Provider: r.GetProvider(), Model: r.GetModel(),
		InputTokens: r.GetInputTokens(), OutputTokens: r.GetOutputTokens(), CachedTokens: r.GetCachedTokens(),
		At: r.GetAt().AsTime(),
	}
}
