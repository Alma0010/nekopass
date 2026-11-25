package control

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func testControlStreamNode(t *testing.T, p *pgxpool.Pool) (int64, string) {
	t.Helper()
	token := Secret()
	var id int64
	if err := p.QueryRow(context.Background(), "INSERT INTO nodes(name,token_hash,token) VALUES($1,$2,$3) RETURNING id", Secret(), Hash(token), token).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = p.Exec(context.Background(), "DELETE FROM nodes WHERE id=$1", id) })
	return id, token
}

func TestControlHeartbeatTimeoutReleasesNode(t *testing.T) {
	p := testDB(t)
	_, token := testControlStreamNode(t, p)
	const timeout = 2 * time.Second
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterControlServer(server, &StreamServer{Server: New(p), reportIdleTimeout: timeout})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///control",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	client := pb.NewControlClient(conn)
	report := &pb.AgentMessage{InstanceId: Secret(), ProtocolVersion: 12}
	connect := func() pb.Control_ConnectClient {
		t.Helper()
		stream, err := client.Connect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return stream
	}
	exchange := func(stream pb.Control_ConnectClient) {
		t.Helper()
		if err := stream.Send(report); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(); err != nil {
			t.Fatal(err)
		}
	}

	owner := connect()
	exchange(owner)
	// Keep reporting past the original deadline to verify that liveness is renewed.
	for i := 0; i < 5; i++ {
		time.Sleep(timeout / 4)
		exchange(owner)
	}
	duplicate := connect()
	if _, err := duplicate.Recv(); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("live owner was not protected: %v", err)
	}
	if _, err := owner.Recv(); status.Code(err) != codes.DeadlineExceeded || status.Convert(err).Message() != "node heartbeat timed out" {
		t.Fatalf("idle stream did not close on its heartbeat deadline: %v", err)
	}
	exchange(connect())
}

type blockedControlReply struct {
	pb.Control_ConnectServer
	ctx        context.Context
	report     *pb.AgentMessage
	replyReady chan struct{}
	received   bool
}

func (s *blockedControlReply) Context() context.Context { return s.ctx }
func (s *blockedControlReply) Recv() (*pb.AgentMessage, error) {
	if !s.received {
		s.received = true
		return s.report, nil
	}
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}
func (s *blockedControlReply) Send(*pb.ControlMessage) error {
	close(s.replyReady)
	<-s.ctx.Done()
	return s.ctx.Err()
}

func TestControlLockExpiresWhenReplyCannotBeSent(t *testing.T) {
	p := testDB(t)
	id, token := testControlStreamNode(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	stream := &blockedControlReply{
		ctx:        metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+token)),
		report:     &pb.AgentMessage{InstanceId: Secret(), ProtocolVersion: 12},
		replyReady: make(chan struct{}),
	}
	server := &StreamServer{Server: New(p), reportIdleTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Connect(stream) }()
	defer func() {
		cancel()
		if err := <-done; err == nil {
			t.Error("blocked stream unexpectedly succeeded")
		}
	}()
	select {
	case <-stream.replyReady:
	case <-ctx.Done():
		t.Fatal("stream did not reach the response write")
	}
	canAcquire := func() bool {
		t.Helper()
		var unlocked bool
		if err := p.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", -id).Scan(&unlocked); err != nil {
			t.Fatal(err)
		}
		return unlocked
	}
	if canAcquire() {
		t.Fatal("stream never held the node lock")
	}
	for !canAcquire() {
		time.Sleep(25 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("lock must expire before the blocked stream is cancelled")
	}
}
