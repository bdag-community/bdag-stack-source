package profiling

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func TestStartHonorsLoopbackListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	Start(address)
	client := &http.Client{Timeout: 100 * time.Millisecond, Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/debug/pprof/")
		if err == nil {
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("profiler status = %d", response.StatusCode)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("profiler did not listen on configured loopback address %s", address)
}
