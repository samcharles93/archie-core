package logrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/natsrpc"
)

const daemonSubject = "archie.logs.archied"

func RegisterDaemon(nc *nats.Conn, feed *logging.Feed, log *slog.Logger) (func(), error) {
	return natsrpc.RegisterAll(nc, []natsrpc.Registration{{Subject: daemonSubject, Handler: func(msg *nats.Msg) {
		var q logging.Query
		if err := json.Unmarshal(msg.Data, &q); err != nil {
			natsrpc.Respond(msg, log, "logs", reply{Error: "invalid log query"})
			return
		}
		result, err := feed.Read(q)
		out := reply{Result: result}
		if err != nil {
			out.Error = err.Error()
		}
		natsrpc.Respond(msg, log, "logs", out)
	}}})
}

type reply struct {
	Result logging.Result
	Error  string
}

func ReadDaemon(ctx context.Context, nc *nats.Conn, q logging.Query) (logging.Result, error) {
	raw, err := json.Marshal(q)
	if err != nil {
		return logging.Result{}, err
	}
	msg, err := nc.RequestWithContext(ctx, daemonSubject, raw)
	if err != nil {
		return logging.Result{}, err
	}
	var out reply
	if err := json.Unmarshal(msg.Data, &out); err != nil {
		return logging.Result{}, err
	}
	if out.Error != "" {
		return logging.Result{}, fmt.Errorf("daemon logs: %s", out.Error)
	}
	return out.Result, nil
}
