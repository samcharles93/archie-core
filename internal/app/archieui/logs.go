package archieui

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/messagingrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/webui"
)

func wireServiceLogs(opts Options, srv *webui.Server, tasks *staterpc.Client, chat *gatewayrpc.Client, feed *logging.Feed) (func(), error) {
	srv.LogFeed = feed
	srv.LogSources = map[string]webui.LogSource{
		presence.UI:         func(_ context.Context, q logging.Query) (logging.Result, error) { return feed.Read(q) },
		presence.StateStore: tasks.RecentLogs,
		presence.Gateway: func(ctx context.Context, q logging.Query) (logging.Result, error) {
			return chat.RecentLogs(ctx, presence.Gateway, q)
		},
		presence.Daemon: func(ctx context.Context, q logging.Query) (logging.Result, error) {
			return chat.RecentLogs(ctx, presence.Daemon, q)
		},
	}
	messaging, closeMessaging, dialErr := messagingrpc.Dial(opts.Messaging.Target, opts.Messaging.Token)
	if dialErr != nil {
		return nil, dialErr
	}
	srv.LogSources[presence.Messaging] = messaging.RecentLogs
	return closeMessaging, nil
}
