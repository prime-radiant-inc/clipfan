package transport

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeListenerClosesListenerBeforeReturningOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv := NewServer(addr, testAuth(t), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ServeListener(ctx, ln) }()

	requireHTTPHealth(t, "http://"+addr+"/v1/health")
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeListener after cancel = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeListener did not return after cancellation")
	}

	conn, dialErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("listener %s still accepts connections after ServeListener returned", addr)
	}
}

func requireHTTPHealth(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("health endpoint %s did not become ready", url)
}
