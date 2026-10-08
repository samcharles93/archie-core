// Package messagingrpc is the transport for messaging.v1.MessagingService:
// the server the Messaging Service registers and the client other services
// dial to deliver outbound chat messages.
package messagingrpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/messaging/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/logging"
)

const tokenMetadataKey = "messaging-token"

// ErrUnavailable marks a delivery the channel could not make now: the channel
// is unknown, stopped or cannot send. The caller may retry later.
var ErrUnavailable = errors.New("messaging: channel unavailable")

// Deliverer sends text to one chat on a named channel.
type Deliverer interface {
	Deliver(ctx context.Context, channel, chatID, text string) error
}

type server struct {
	pb.UnimplementedMessagingServiceServer
	deliverer Deliverer
	feed      *logging.Feed
}

// RegisterServer registers MessagingService over d.
func RegisterServer(registrar grpc.ServiceRegistrar, d Deliverer, feed *logging.Feed) {
	pb.RegisterMessagingServiceServer(registrar, &server{deliverer: d, feed: feed})
}

func (s *server) Deliver(ctx context.Context, req *pb.DeliverRequest) (*pb.DeliverResponse, error) {
	if req.GetChannel() == "" || req.GetChatId() == "" || req.GetText() == "" {
		return nil, status.Error(codes.InvalidArgument, "channel, chat_id and text are required")
	}
	err := s.deliverer.Deliver(ctx, req.GetChannel(), req.GetChatId(), req.GetText())
	switch {
	case err == nil:
		return &pb.DeliverResponse{}, nil
	case errors.Is(err, ErrUnavailable):
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	default:
		return nil, status.Error(codes.Unavailable, err.Error())
	}
}

// ServerOptions serves a loopback listen address without credentials and
// requires token on any other, failing closed when it is empty.
func ServerOptions(listen, token string) ([]grpc.ServerOption, error) {
	loopback, err := gatewayrpc.TargetIsLoopback(listen)
	if err != nil {
		return nil, err
	}
	if loopback {
		return nil, nil
	}
	if token == "" {
		return nil, fmt.Errorf("messaging listen address %q is non-loopback and requires a bearer token", listen)
	}
	return []grpc.ServerOption{grpc.ChainUnaryInterceptor(requireToken(token))}, nil
}

func requireToken(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		values := metadata.ValueFromIncomingContext(ctx, tokenMetadataKey)
		if len(values) == 0 || subtle.ConstantTimeCompare([]byte(token), []byte(values[0])) != 1 {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid messaging token")
		}
		return handler(ctx, req)
	}
}

// Client delivers through a dialled MessagingService.
type Client struct{ rpc pb.MessagingServiceClient }

// Dial returns a client for target. A non-loopback target requires token.
func Dial(target, token string) (*Client, func(), error) {
	target = strings.TrimSpace(target)
	loopback, err := gatewayrpc.TargetIsLoopback(target)
	if err != nil {
		return nil, nil, fmt.Errorf("messaging target must be host:port: %w", err)
	}
	if !loopback && token == "" {
		return nil, nil, fmt.Errorf("messaging target %q is non-loopback and requires a bearer token ([services.messaging].target_token / MESSAGING_TOKEN)", target)
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if token != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, tokenMetadataKey, token), method, req, reply, cc, opts...)
		}))
	}
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create messaging client: %w", err)
	}
	return &Client{rpc: pb.NewMessagingServiceClient(conn)}, func() { _ = conn.Close() }, nil
}

// Deliver sends text to chatID on channel.
func (c *Client) Deliver(ctx context.Context, channel, chatID, text string) error {
	_, err := c.rpc.Deliver(ctx, &pb.DeliverRequest{Channel: channel, ChatId: chatID, Text: text})
	return err
}
