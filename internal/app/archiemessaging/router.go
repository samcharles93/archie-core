package archiemessaging

import (
	"context"
	"errors"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type contractTaskController struct {
	chat messaging.ChatContract
}

func (c *contractTaskController) Approve(ctx context.Context, taskID int64, identity string) error {
	_, err := c.chat.ApplyTaskAction(ctx, identity, taskID, taskstate.ActionApprove)
	return err
}

func (c *contractTaskController) Cancel(ctx context.Context, taskID int64, identity string) error {
	_, err := c.chat.ApplyTaskAction(ctx, identity, taskID, taskstate.ActionCancel)
	return err
}

func (c *contractTaskController) StopRunning(ctx context.Context, identity string) ([]int64, error) {
	return nil, nil
}

// NewContractRouter builds a gateway.Router that forwards all turn dispatch,
// streaming, and task mutations to Gateway's ChatContract over gRPC.
func NewContractRouter(chat messaging.ChatContract, channelName string) *gateway.Router {
	router := gateway.NewRouter(nil, nil, channelName)
	router.Controller = &contractTaskController{chat: chat}

	router.LLM = func(ctx context.Context, in gateway.Inbound) (string, error) {
		reply, err := chat.Route(ctx, in)
		if err != nil {
			return "", err
		}
		return reply.Text, nil
	}

	router.LLMStream = func(ctx context.Context, in gateway.Inbound, stream gateway.TurnStream) (string, error) {
		events, err := chat.Stream(ctx, in)
		if err != nil {
			return "", err
		}
		var full strings.Builder
		for ev := range events {
			switch ev.Kind {
			case "started":
				// Generation began
			case "delta":
				if stream != nil {
					stream.Delta(ev.Text)
				}
				full.WriteString(ev.Text)
			case "tool":
				if stream != nil {
					stream.ToolCall(ev.Tool)
				}
			case "media":
				if stream != nil {
					stream.Media(ev.Media)
				}
			case "done":
				return ev.Text, nil
			case "error":
				return "", errors.New(ev.Text)
			}
		}
		return full.String(), nil
	}

	return router
}
