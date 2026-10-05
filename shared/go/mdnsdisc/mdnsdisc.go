// Package mdnsdisc advertises and browses Bonjour/mDNS services
// (_edgehop._tcp, _reach._tcp) on top of github.com/grandcat/zeroconf.
//
// TXT records are exchanged as key=value maps. Browsing reports a service once
// when it appears and once more with Added=false when it has stopped answering
// queries: zeroconf v1.0.0 never surfaces goodbye packets, so Browse re-queries
// in cycles of BrowseCycle and treats a service missing from two consecutive
// cycles as gone.
package mdnsdisc

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

// Domain is the mDNS domain used for every service.
const Domain = "local."

// BrowseCycle is the length of one browse cycle. Every cycle sends a fresh
// query; a service must answer within the cycle to count as present.
var BrowseCycle = 30 * time.Second

// MissedCycles is how many consecutive cycles a service may stay silent before
// Browse reports it as removed.
var MissedCycles = 2

// Event describes a service appearing (Added) or disappearing.
type Event struct {
	Added    bool
	Instance string
	Host     string
	Port     int
	Addrs    []net.IP
	TXT      map[string]string
}

// TXT converts a map to "k=v" records in key order (what zeroconf expects).
func TXT(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// ParseTXT converts "k=v" records to a map; records without "=" map to "".
func ParseTXT(records []string) map[string]string {
	m := make(map[string]string, len(records))
	for _, r := range records {
		if r == "" {
			continue
		}
		k, v, _ := strings.Cut(r, "=")
		m[k] = v
	}
	return m
}

// Advertise registers instance of service (for example "_edgehop._tcp") on
// port with the given TXT map on every multicast-capable interface. The
// returned function withdraws the registration; it is also called when ctx
// ends. Errors (typically no multicast interface) are returned immediately.
func Advertise(ctx context.Context, service, instance string, port int, txt map[string]string) (func(), error) {
	srv, err := zeroconf.Register(instance, service, Domain, port, TXT(txt), nil)
	if err != nil {
		return nil, fmt.Errorf("mdnsdisc: advertise %s: %w", service, err)
	}
	var once sync.Once
	done := make(chan struct{})
	stop := func() {
		once.Do(func() {
			srv.Shutdown()
			close(done)
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		}
	}()
	return stop, nil
}

// Browse watches service until ctx ends, calling on for every appearance and
// disappearance (see the package comment for the removal rule). It blocks;
// run it in its own goroutine. It returns nil when ctx ends and an error when
// the resolver cannot be created (no multicast interface) or a query fails.
func Browse(ctx context.Context, service string, on func(Event)) error {
	known := map[string]Event{}
	missed := map[string]int{}
	for {
		seen, err := browseCycle(ctx, service, func(ev Event) {
			old, ok := known[ev.Instance]
			if !ok || !sameService(old, ev) {
				known[ev.Instance] = ev
				on(ev)
			}
		})
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		for inst, ev := range known {
			if seen[inst] {
				delete(missed, inst)
				continue
			}
			missed[inst]++
			if missed[inst] >= MissedCycles {
				delete(known, inst)
				delete(missed, inst)
				ev.Added = false
				on(ev)
			}
		}
	}
}

// browseCycle runs one query cycle and returns the instances that answered.
func browseCycle(ctx context.Context, service string, on func(Event)) (map[string]bool, error) {
	cycleCtx, cancel := context.WithTimeout(ctx, BrowseCycle)
	defer cancel()
	r, err := newResolver()
	if err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	if err := r.Browse(cycleCtx, service, Domain, entries); err != nil {
		return nil, fmt.Errorf("mdnsdisc: browse %s: %w", service, err)
	}
	seen := map[string]bool{}
	for {
		select {
		case <-cycleCtx.Done():
			return seen, nil
		case e, ok := <-entries:
			if !ok {
				<-cycleCtx.Done()
				return seen, nil
			}
			if e == nil || e.Instance == "" {
				continue
			}
			ev := toEvent(e)
			seen[ev.Instance] = true
			on(ev)
		}
	}
}

// newResolver joins both multicast groups, falling back to IPv4-only and then
// IPv6-only hosts (a container without IPv6 fails the dual-stack resolver).
func newResolver() (*zeroconf.Resolver, error) {
	var firstErr error
	for _, opt := range []zeroconf.ClientOption{nil, zeroconf.SelectIPTraffic(zeroconf.IPv4), zeroconf.SelectIPTraffic(zeroconf.IPv6)} {
		r, err := zeroconf.NewResolver(opt)
		if err == nil {
			return r, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, fmt.Errorf("mdnsdisc: resolver: %w", firstErr)
}

func toEvent(e *zeroconf.ServiceEntry) Event {
	addrs := make([]net.IP, 0, len(e.AddrIPv4)+len(e.AddrIPv6))
	addrs = append(addrs, e.AddrIPv4...)
	addrs = append(addrs, e.AddrIPv6...)
	return Event{
		Added:    true,
		Instance: e.Instance,
		Host:     strings.TrimSuffix(e.HostName, "."),
		Port:     e.Port,
		Addrs:    addrs,
		TXT:      ParseTXT(e.Text),
	}
}

// sameService reports whether two events describe the same endpoint, so a
// re-announcement with unchanged details is not reported twice.
func sameService(a, b Event) bool {
	if a.Host != b.Host || a.Port != b.Port || len(a.TXT) != len(b.TXT) || len(a.Addrs) != len(b.Addrs) {
		return false
	}
	for k, v := range a.TXT {
		if b.TXT[k] != v {
			return false
		}
	}
	for i := range a.Addrs {
		if !a.Addrs[i].Equal(b.Addrs[i]) {
			return false
		}
	}
	return true
}
