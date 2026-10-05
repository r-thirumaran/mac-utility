package mdnsdisc

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestTXTRoundTrip(t *testing.T) {
	m := map[string]string{"v": "1", "id": "abc", "name": "Thiru's Mac", "pk": "a=b=="}
	recs := TXT(m)
	want := []string{"id=abc", "name=Thiru's Mac", "pk=a=b==", "v=1"}
	if !reflect.DeepEqual(recs, want) {
		t.Fatalf("TXT = %q", recs)
	}
	if got := ParseTXT(recs); !reflect.DeepEqual(got, m) {
		t.Fatalf("ParseTXT = %v", got)
	}
	if got := ParseTXT([]string{"flag", ""}); !reflect.DeepEqual(got, map[string]string{"flag": ""}) {
		t.Fatalf("flag record = %v", got)
	}
	if len(TXT(nil)) != 0 {
		t.Fatal("TXT(nil) not empty")
	}
}

func TestAdvertiseBrowseLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	old := BrowseCycle
	BrowseCycle = 2 * time.Second
	defer func() { BrowseCycle = old }()

	host, _ := os.Hostname()
	instance := fmt.Sprintf("mutest-%d", os.Getpid())
	stop, err := Advertise(ctx, "_mutest._tcp", instance, 4321, map[string]string{"v": "1", "id": "abc"})
	if err != nil {
		t.Skipf("multicast unavailable: %v", err)
	}
	defer stop()

	events := make(chan Event, 16)
	bctx, bcancel := context.WithCancel(ctx)
	defer bcancel()
	errc := make(chan error, 1)
	go func() { errc <- Browse(bctx, "_mutest._tcp", func(e Event) { events <- e }) }()

	var found Event
	deadline := time.After(3 * time.Second)
wait:
	for {
		select {
		case err := <-errc:
			t.Skipf("multicast unavailable: browse: %v", err)
		case e := <-events:
			if e.Instance == instance {
				found = e
				break wait
			}
		case <-deadline:
			t.Skipf("no mDNS answer within 3 s (host %s); multicast unavailable here", host)
		}
	}
	if !found.Added || found.Port != 4321 || found.TXT["v"] != "1" || found.TXT["id"] != "abc" {
		t.Fatalf("event = %+v", found)
	}
	if len(found.Addrs) == 0 || found.Host == "" {
		t.Fatalf("event lacks address/host: %+v", found)
	}

	// Withdraw the service: it must be reported as removed after it stops
	// answering for MissedCycles cycles.
	stop()
	removal := time.After(time.Duration(MissedCycles+2) * BrowseCycle)
	for {
		select {
		case e := <-events:
			if e.Instance == instance && !e.Added {
				if e.Port != 4321 {
					t.Fatalf("removal event = %+v", e)
				}
				return
			}
		case <-removal:
			t.Fatal("service was not reported as removed")
		}
	}
}
