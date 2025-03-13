package ws_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/paskozdilar/grpc-gateway-websocket/example"
	"github.com/paskozdilar/grpc-gateway-websocket/ws"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	serverPort  = 8080
	gatewayPort = 8081
)

func TestMain(m *testing.M) {
	wg := &sync.WaitGroup{}
	defer wg.Wait()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runServer(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runGateway(ctx)
	}()

	waitForGateway()

	m.Run()
}

type impl struct {
	example.UnsafeExampleServiceServer
}

func (*impl) Unary(ctx context.Context, req *example.ExampleMessage) (*example.ExampleMessage, error) {
	return &example.ExampleMessage{Data: req.Data}, nil
}

func (*impl) ClientStream(stream example.ExampleService_ClientStreamServer) error {
	var data string
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&example.ExampleMessage{Data: data})
		}
		if err != nil {
			return err
		}
		data = msg.Data
	}
}

func (*impl) ServerStream(req *example.ExampleMessage, stream example.ExampleService_ServerStreamServer) error {
	for i := range 10 {
		if err := stream.Send(&example.ExampleMessage{Data: fmt.Sprintf("%s %d", req.Data, i)}); err != nil {
			return err
		}
	}
	return nil
}

func (*impl) Bidirectional(stream example.ExampleService_BidirectionalServer) error {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&example.ExampleMessage{Data: msg.Data}); err != nil {
			return err
		}
	}
}

func (*impl) NoBodyUnary(ctx context.Context, _ *example.ExampleMessage) (*example.ExampleMessage, error) {
	return &example.ExampleMessage{Data: "hello"}, nil
}

func (*impl) NoBodyClientStream(stream example.ExampleService_NoBodyClientStreamServer) error {
	var data string
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&example.ExampleMessage{Data: data})
		}
		if err != nil {
			return err
		}
		data = msg.Data
	}
}

func (*impl) NoBodyServerStream(_ *example.ExampleMessage, stream example.ExampleService_NoBodyServerStreamServer) error {
	for i := range 10 {
		if err := stream.Send(&example.ExampleMessage{Data: fmt.Sprintf("%s %d", "hello", i)}); err != nil {
			return err
		}
	}
	return nil
}

func (*impl) NoBodyBidirectional(stream example.ExampleService_NoBodyBidirectionalServer) error {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&example.ExampleMessage{Data: msg.Data}); err != nil {
			return err
		}
	}
}

func runServer(ctx context.Context) {
	s := grpc.NewServer()
	example.RegisterExampleServiceServer(s, &impl{})

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", serverPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()

	go func() {
		<-ctx.Done()
		s.GracefulStop()
	}()

	s.Serve(l)
}

func runGateway(ctx context.Context) {
	mux := runtime.NewServeMux()
	example.RegisterExampleServiceHandlerFromEndpoint(
		ctx,
		mux,
		fmt.Sprintf("localhost:%d", serverPort),
		[]grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	)
	hmux := http.NewServeMux()
	hmux.Handle("/example/", hmux)
	hmux.Handle("/healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	wsmux := ws.Gateway(mux)

	s := &http.Server{
		Addr:    fmt.Sprintf(":%d", gatewayPort),
		Handler: wsmux,
	}
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	s.ListenAndServe()
}

func waitForGateway() {
	for {
		_, err := http.Get(fmt.Sprintf("http://localhost:%d/healthz", gatewayPort))
		if err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUnary(t *testing.T) {
	url := fmt.Sprintf("ws://localhost:%d/example/v1/Unary", gatewayPort)

	conn, _, err := (&websocket.Dialer{}).Dial(url, nil)
	if err != nil {
		t.Fatalf("websocket.Dialer.Dial() failed: %v, want success", err)
	}
	defer conn.Close()

	msg := &example.ExampleMessage{Data: "hello"}
	if err := conn.WriteJSON(&msg); err != nil {
		t.Fatalf("conn.WriteJSON() failed: %v, want success", err)
	}

	msg = &example.ExampleMessage{}
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("conn.ReadJSON() failed: %v, want success", err)
	}
	if msg.Data != "hello" {
		t.Fatalf("msg.Data = %q, want %q", msg.Data, "hello")
	}

	err = conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)
	if err != nil {
		t.Fatalf("conn.WriteMessage() failed: %v, want success", err)
	}

	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("conn.ReadMessage() failed: %v, want io.EOF", err)
	}
}

func TestClientStream(t *testing.T) {
	url := fmt.Sprintf("ws://localhost:%d/example/v1/ClientStream", gatewayPort)

	conn, _, err := (&websocket.Dialer{}).Dial(url, nil)
	if err != nil {
		t.Fatalf("websocket.Dialer.Dial() failed: %v, want success", err)
	}
	defer conn.Close()

	for i := range 10 {
		msg := &example.ExampleMessage{Data: fmt.Sprintf("hello %d", i)}
		if err := conn.WriteJSON(&msg); err != nil {
			t.Fatalf("conn.WriteJSON() failed: %v, want success", err)
		}
	}
	conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)

	msg := &example.ExampleMessage{}
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("conn.ReadJSON() failed: %v, want success", err)
	}
	if msg.Data != "hello 9" {
		t.Fatalf("msg.Data = %q, want %q", msg.Data, "hello")
	}
}

func TestServerStream(t *testing.T) {
	url := fmt.Sprintf("ws://localhost:%d/example/v1/ServerStream", gatewayPort)

	conn, _, err := (&websocket.Dialer{}).Dial(url, nil)
	if err != nil {
		t.Fatalf("websocket.Dialer.Dial() failed: %v, want success", err)
	}
	defer conn.Close()

	err = conn.WriteJSON(&example.ExampleMessage{Data: "hello"})
	if err != nil {
		t.Fatalf("conn.WriteJSON() failed: %v, want success", err)
	}

	// grpc-gateway wraps streaming responses in {"result": ...} JSON
	type Response struct {
		Result *example.ExampleMessage
	}

	for i := range 10 {
		msg := &example.ExampleMessage{}
		if err := conn.ReadJSON(&Response{Result: msg}); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("conn.ReadJSON() failed: %v, want success", err)
		}
		if msg.Data != fmt.Sprintf("hello %d", i) {
			t.Fatalf("msg.Data = %q, want %q", msg.Data, fmt.Sprintf("hello %d", i))
		}
	}

	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("conn.ReadMessage() failed: %v, want io.EOF", err)
	}
}

func TestBidirectional(t *testing.T) {
	url := fmt.Sprintf("ws://localhost:%d/example/v1/Bidirectional", gatewayPort)

	conn, _, err := (&websocket.Dialer{}).Dial(url, nil)
	if err != nil {
		t.Fatalf("websocket.Dialer.Dial() failed: %v, want success", err)
	}
	defer conn.Close()

	for i := range 10 {
		msg := &example.ExampleMessage{Data: fmt.Sprintf("hello %d", i)}
		if err := conn.WriteJSON(&msg); err != nil {
			t.Fatalf("conn.WriteJSON() failed: %v, want success", err)
		}

		// grpc-gateway wraps streaming responses in {"result": ...} JSON
		type Response struct {
			Result *example.ExampleMessage
		}

		msg = &example.ExampleMessage{}
		if err := conn.ReadJSON(&Response{Result: msg}); err != nil {
			t.Fatalf("conn.ReadJSON() failed: %v, want success", err)
		}
		if msg.Data != fmt.Sprintf("hello %d", i) {
			t.Fatalf("msg.Data = %q, want %q", msg.Data, fmt.Sprintf("hello %d", i))
		}
	}

	err = conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)
	if err != nil {
		t.Fatalf("conn.WriteMessage() failed: %v, want success", err)
	}

	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("conn.ReadMessage() failed: %v, want io.EOF", err)
	}
}

func TestNoBodyUnary(t *testing.T) {
	url := fmt.Sprintf("ws://localhost:%d/example/v1/NoBodyUnary", gatewayPort)
	done := make(chan error)

	go func() {
		defer close(done)

		conn, _, err := (&websocket.Dialer{}).Dial(url, nil)
		if err != nil {
			done <- fmt.Errorf("websocket.Dialer.Dial() failed: %v, want success", err)
			return
		}
		defer conn.Close()

		err = conn.WriteMessage(websocket.TextMessage, []byte("{}"))
		if err != nil {
			done <- fmt.Errorf("conn.WriteMessage() failed: %v, want success", err)
			return
		}

		msg := &example.ExampleMessage{}
		if err := conn.ReadJSON(&msg); err != nil {
			done <- fmt.Errorf("conn.ReadJSON() failed: %v, want success", err)
			return
		}
		if msg.Data != "hello" {
			done <- fmt.Errorf("msg.Data = %q, want %q", msg.Data, "hello")
			return
		}

		_, _, err = conn.ReadMessage()
		if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			done <- fmt.Errorf("conn.ReadMessage() failed: %v, want io.EOF", err)
			return
		}
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestNoBodyClientStream(t *testing.T) {
	url := fmt.Sprintf("ws://localhost:%d/example/v1/NoBodyClientStream", gatewayPort)
	done := make(chan error)

	go func() {
		defer close(done)

		conn, _, err := (&websocket.Dialer{}).Dial(url, nil)
		if err != nil {
			done <- fmt.Errorf("websocket.Dialer.Dial() failed: %v, want success", err)
			return
		}
		defer conn.Close()

		for i := range 10 {
			msg := &example.ExampleMessage{Data: fmt.Sprintf("hello %d", i)}
			err := conn.WriteJSON(&msg)
			if err != nil {
				done <- fmt.Errorf("conn.WriteMessage() failed: %v, want success", err)
				return
			}
		}

		conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)

		msg := &example.ExampleMessage{}
		if err := conn.ReadJSON(&msg); err != nil {
			done <- fmt.Errorf("conn.ReadJSON() failed: %v, want success", err)
			return
		}
		if msg.Data != "hello 9" {
			done <- fmt.Errorf("msg.Data = %q, want %q", msg.Data, "hello")
			return
		}
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestNoBodyServerStream(t *testing.T) {
	url := fmt.Sprintf("ws://localhost:%d/example/v1/NoBodyServerStream", gatewayPort)
	done := make(chan error)

	go func() {
		defer close(done)

		conn, _, err := (&websocket.Dialer{}).Dial(url, nil)
		if err != nil {
			done <- fmt.Errorf("websocket.Dialer.Dial() failed: %v, want success", err)
			return
		}
		defer conn.Close()

		err = conn.WriteMessage(websocket.TextMessage, []byte("{}"))
		if err != nil {
			done <- fmt.Errorf("conn.WriteMessage() failed: %v, want success", err)
			return
		}

		// grpc-gateway wraps streaming responses in {"result": ...} JSON
		type Response struct {
			Result *example.ExampleMessage
		}

		for i := range 10 {
			msg := &example.ExampleMessage{}
			if err := conn.ReadJSON(&Response{Result: msg}); err != nil {
				if err == io.EOF {
					break
				}
				done <- fmt.Errorf("conn.ReadJSON() failed: %v, want success", err)
				return
			}
			if msg.Data != fmt.Sprintf("hello %d", i) {
				done <- fmt.Errorf("msg.Data = %q, want %q", msg.Data, fmt.Sprintf("hello %d", i))
				return
			}
		}

		_, _, err = conn.ReadMessage()
		if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			done <- fmt.Errorf("conn.ReadMessage() failed: %v, want io.EOF", err)
			return
		}
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestNoBodyBidirectional(t *testing.T) {
	url := fmt.Sprintf("ws://localhost:%d/example/v1/NoBodyBidirectional", gatewayPort)
	done := make(chan error)

	go func() {
		defer close(done)

		conn, _, err := (&websocket.Dialer{}).Dial(url, nil)
		if err != nil {
			done <- fmt.Errorf("websocket.Dialer.Dial() failed: %v, want success", err)
			return
		}
		defer conn.Close()

		for i := range 10 {
			err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"data": "hello %d"}`, i)))
			if err != nil {
				done <- fmt.Errorf("conn.WriteMessage() failed: %v, want success", err)
				return
			}

			// grpc-gateway wraps streaming responses in {"result": ...} JSON
			type Response struct {
				Result *example.ExampleMessage
			}

			msg := &example.ExampleMessage{}
			if err := conn.ReadJSON(&Response{Result: msg}); err != nil {
				done <- fmt.Errorf("conn.ReadJSON() failed: %v, want success", err)
				return
			}
			if msg.Data != fmt.Sprintf("hello %d", i) {
				done <- fmt.Errorf("msg.Data = %q, want %q", msg.Data, fmt.Sprintf("hello %d", i))
				return
			}
		}

		err = conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		if err != nil {
			done <- fmt.Errorf("conn.WriteMessage() failed: %v, want success", err)
			return
		}

		_, _, err = conn.ReadMessage()
		if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			done <- fmt.Errorf("conn.ReadMessage() failed: %v, want io.EOF", err)
			return
		}
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
