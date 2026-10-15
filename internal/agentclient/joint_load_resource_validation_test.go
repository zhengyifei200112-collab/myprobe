package agentclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestJointResourceSamplingContinuesDuringBlockedRequest(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { close(started); <-release; w.WriteHeader(200) }))
	defer server.Close()
	// Release before server.Close even if a test assertion fails.
	defer releaseOnce.Do(func() { close(release) })
	go func() {
		defer close(finished)
		resp, err := http.Get(server.URL)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	observed := make(chan struct{})
	reads := 0
	sampler := startJointResourceSampler(context.Background(), time.Now(), 10*time.Millisecond, func() jointResourceReading {
		reads++
		if reads == 4 {
			close(observed)
		}
		if reads == 3 {
			return jointResourceReading{rss: 100, wal: 200, cpu: 30, serverAvailable: true}
		}
		return jointResourceReading{rss: 10, wal: 20, cpu: 10, serverAvailable: reads != 2}
	})
	defer sampler.stop()
	select {
	case <-observed:
	case <-time.After(3 * time.Second):
		t.Fatal("sampling blocked with HTTP request")
	}
	select {
	case <-finished:
		t.Fatal("request unexpectedly finished")
	default:
	}
	stats := sampler.stop()
	if stats.Samples < 4 || stats.PeakRSSBytes != 100 || stats.PeakWALBytes != 200 || stats.ServerUnavailableSamples != 1 || stats.MaximumGapSeconds <= 0 {
		t.Fatalf("observation = %+v", stats)
	}
	if len(stats.Timeline) < 2 || stats.Timeline[len(stats.Timeline)-1].SampledPeakWALBytes != 200 {
		t.Fatal("final resource timeline lost sampled peak")
	}
	releaseOnce.Do(func() { close(release) })
	<-finished
}

func TestJointResourceTargetRestartAndConcurrentReads(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for path, value := range map[string]string{first: "usage_usec 23\n", second: "usage_usec 7\n"} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "cpu.stat"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	database := filepath.Join(root, "fixture.db")
	if err := os.WriteFile(database+"-wal", make([]byte, 64), 0600); err != nil {
		t.Fatal(err)
	}
	a := jointServer{pid: os.Getpid(), cgroup: first, database: database}
	b := jointServer{pid: os.Getpid(), cgroup: second, database: database}
	target := &jointResourceTarget{}
	target.set(&a, 100)
	if reading := target.read(); reading.cpu != 123 || reading.wal != 64 {
		t.Fatalf("first: %+v", reading)
	}
	target.set(nil, 123)
	if reading := target.read(); reading.cpu != 123 || reading.serverAvailable || reading.wal != 64 {
		t.Fatalf("restart: %+v", reading)
	}
	target.set(&b, 123)
	if reading := target.read(); reading.cpu != 130 {
		t.Fatalf("second: %+v", reading)
	}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for range 100 {
			target.read()
		}
	}()
	for range 100 {
		target.set(&a, 100)
		target.set(nil, 123)
		target.set(&b, 123)
	}
	workers.Wait()
}
