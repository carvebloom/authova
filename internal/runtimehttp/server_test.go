package runtimehttp

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestShutdownWaitsForInflightResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	server := &http.Server{Addr: address, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.Write([]byte("committed")) })}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, server) }()
	responseDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		for attempt := 0; attempt < 100; attempt++ {
			response, err := client.Get("http://" + address)
			if err == nil {
				response.Body.Close()
				responseDone <- nil
				return
			}
			time.Sleep(time.Millisecond)
		}
		responseDone <- context.DeadlineExceeded
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("listener unavailable")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("process returned before inflight handler drained")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-responseDone; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
