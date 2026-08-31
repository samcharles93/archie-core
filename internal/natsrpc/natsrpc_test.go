package natsrpc

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

func startEmbedded(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	return srv
}

func connect(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func TestNewEnvelopeAndErr(t *testing.T) {
	if e := NewEnvelope(nil); e.Error != "" {
		t.Fatalf("NewEnvelope(nil).Error = %q, want empty", e.Error)
	}
	if err := (Envelope{}).Err(); err != nil {
		t.Fatalf("Envelope{}.Err() = %v, want nil", err)
	}

	wantErr := "boom"
	e := NewEnvelope(errString(wantErr))
	if e.Error != wantErr {
		t.Fatalf("NewEnvelope(err).Error = %q, want %q", e.Error, wantErr)
	}
	if got := e.Err(); got == nil || got.Error() != wantErr {
		t.Fatalf("Err() = %v, want error %q", got, wantErr)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestClientRequest(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	sub, err := nc.Subscribe("echo.subject", func(msg *nats.Msg) {
		_ = msg.Respond(append([]byte("echo:"), msg.Data...))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	c := &Client{Conn: nc, Timeout: 2 * time.Second}
	reply, err := c.Request(context.Background(), "echo.subject", []byte("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if string(reply) != "echo:hi" {
		t.Fatalf("reply = %q, want %q", reply, "echo:hi")
	}
}

func TestClientRequestNoResponders(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	c := &Client{Conn: nc, Timeout: 200 * time.Millisecond}
	if _, err := c.Request(context.Background(), "nobody.listening", []byte("hi")); err == nil {
		t.Fatal("Request() error = nil, want error when no responder is subscribed")
	}
}

func TestClientRequestUsesContextDeadlineWhenSet(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	c := &Client{Conn: nc, Timeout: 10 * time.Second} // large default, ctx deadline should win
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Request(ctx, "nobody.listening.either", []byte("hi"))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Request() error = nil, want error (no responder)")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Request() took %v, want it to respect the short context deadline rather than the 10s client timeout", elapsed)
	}
}

type callReq struct {
	Value string `json:"value"`
}

type callResp struct {
	Envelope
	Echo string `json:"echo"`
}

func TestCallMarshalsRequestAndUnmarshalsResponse(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	sub, err := nc.Subscribe("call.subject", func(msg *nats.Msg) {
		var req callReq
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			_ = msg.Respond([]byte(`{"error":"bad request"}`))
			return
		}
		resp := callResp{Echo: req.Value}
		data, _ := json.Marshal(resp)
		_ = msg.Respond(data)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	c := &Client{Conn: nc, Timeout: 2 * time.Second}
	resp, err := Call[callResp](context.Background(), c, "call.subject", callReq{Value: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Echo != "hello" {
		t.Fatalf("resp.Echo = %q, want %q", resp.Echo, "hello")
	}
	if err := resp.Err(); err != nil {
		t.Fatalf("resp.Err() = %v, want nil", err)
	}
}

func TestCallPropagatesEnvelopeError(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	sub, err := nc.Subscribe("call.error", func(msg *nats.Msg) {
		_ = msg.Respond([]byte(`{"error":"handler failed"}`))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	c := &Client{Conn: nc, Timeout: 2 * time.Second}
	resp, err := Call[callResp](context.Background(), c, "call.error", callReq{Value: "x"})
	if err != nil {
		t.Fatalf("Call() transport error = %v, want nil (envelope errors are not transport errors)", err)
	}
	if envErr := resp.Err(); envErr == nil || envErr.Error() != "handler failed" {
		t.Fatalf("resp.Err() = %v, want \"handler failed\"", envErr)
	}
}

func TestCallUnmarshalError(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	sub, err := nc.Subscribe("call.badjson", func(msg *nats.Msg) {
		_ = msg.Respond([]byte("not json"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	c := &Client{Conn: nc, Timeout: 2 * time.Second}
	if _, err := Call[callResp](context.Background(), c, "call.badjson", callReq{Value: "x"}); err == nil {
		t.Fatal("Call() error = nil, want decode error for invalid JSON reply")
	}
}

func TestCallMarshalError(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())
	c := &Client{Conn: nc, Timeout: 2 * time.Second}

	// A channel cannot be marshalled to JSON, so Call must fail before ever
	// sending a request.
	if _, err := Call[callResp](context.Background(), c, "call.never-reached", make(chan int)); err == nil {
		t.Fatal("Call() error = nil, want encode error for unmarshalable request")
	}
}

func TestRegisterAllSubscribesAndFlushes(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	var calledA, calledB bool
	regs := []Registration{
		{Subject: "reg.a", Handler: func(msg *nats.Msg) { calledA = true; _ = msg.Respond(nil) }},
		{Subject: "reg.b", Handler: func(msg *nats.Msg) { calledB = true; _ = msg.Respond(nil) }},
	}

	unsubscribe, err := RegisterAll(nc, regs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unsubscribe)

	if _, err := nc.Request("reg.a", nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := nc.Request("reg.b", nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if !calledA || !calledB {
		t.Fatalf("calledA=%t calledB=%t, want both true", calledA, calledB)
	}

	unsubscribe()
	if _, err := nc.Request("reg.a", nil, 200*time.Millisecond); err == nil {
		t.Fatal("Request() after unsubscribe error = nil, want no-responder error")
	}
}

func TestRegisterAllRollsBackOnPartialFailure(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	regs := []Registration{
		{Subject: "reg.ok", Handler: func(msg *nats.Msg) { _ = msg.Respond(nil) }},
		// An empty subject is invalid and Subscribe rejects it, forcing the
		// rollback path.
		{Subject: "", Handler: func(msg *nats.Msg) {}},
	}

	if _, err := RegisterAll(nc, regs); err == nil {
		t.Fatal("RegisterAll() error = nil, want error for invalid subject")
	}

	// The first, valid subscription must have been rolled back too.
	if _, err := nc.Request("reg.ok", nil, 200*time.Millisecond); err == nil {
		t.Fatal("Request(\"reg.ok\") after rollback error = nil, want no-responder error")
	}
}

func TestRespondSuccess(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	sub, err := nc.Subscribe("respond.subject", func(msg *nats.Msg) {
		Respond(msg, slog.New(slog.DiscardHandler), "test", callResp{Echo: "ok"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	reply, err := nc.Request("respond.subject", nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var resp callResp
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Echo != "ok" {
		t.Fatalf("resp.Echo = %q, want %q", resp.Echo, "ok")
	}
}

func TestRespondEncodeFailureDoesNotPanic(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	sub, err := nc.Subscribe("respond.badvalue", func(msg *nats.Msg) {
		// A channel cannot be marshalled to JSON; Respond must log and
		// return rather than respond or panic.
		Respond(msg, slog.New(slog.DiscardHandler), "test", make(chan int))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	if _, err := nc.Request("respond.badvalue", nil, 200*time.Millisecond); err == nil {
		t.Fatal("Request() error = nil, want timeout since Respond never replies on encode failure")
	}
}

func TestRespondWithNilLoggerDoesNotPanic(t *testing.T) {
	srv := startEmbedded(t)
	nc := connect(t, srv.ClientURL())

	sub, err := nc.Subscribe("respond.nillogger", func(msg *nats.Msg) {
		Respond(msg, nil, "test", make(chan int))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	// Just confirm the subscriber survives a nil logger without panicking;
	// no reply is expected on encode failure.
	if _, err := nc.Request("respond.nillogger", nil, 200*time.Millisecond); err == nil {
		t.Fatal("Request() error = nil, want timeout")
	}
}
