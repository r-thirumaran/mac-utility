package main

import (
	"context"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/r-thirumaran/mac-utility/shared/go/mdnsdisc"
)

// serviceType is the Bonjour type of docs/ARCHITECTURE.md §8.
const serviceType = "_reach._tcp"

// retryDiscovery is the pause before mDNS is retried after an error (for
// example no multicast interface on a CI box).
var retryDiscovery = time.Minute

// peer is one row of /api/peers.
type peer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

// discovery advertises this agent and keeps the table of the other agents
// seen on the LAN.
type discovery struct {
	mu     sync.Mutex
	peers  map[string]peer // by mDNS instance
	selfID string
	name   chan string // re-advertise with a new name
}

func newDiscovery(selfID string) *discovery {
	return &discovery{peers: map[string]peer{}, selfID: selfID, name: make(chan string, 1)}
}

// list returns the peers sorted by name.
func (d *discovery) list() []peer {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]peer, 0, len(d.peers))
	for _, p := range d.peers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (d *discovery) handle(e mdnsdisc.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !e.Added {
		delete(d.peers, e.Instance)
		return
	}
	id := e.TXT["id"]
	if id == "" || id == d.selfID {
		return
	}
	port := e.Port
	if p, err := strconv.Atoi(e.TXT["port"]); err == nil && p > 0 {
		port = p
	}
	host := e.Host
	for _, ip := range e.Addrs {
		if v4 := ip.To4(); v4 != nil && !v4.IsLoopback() && !v4.IsLinkLocalUnicast() {
			host = v4.String()
			break
		}
	}
	if host == "" {
		return
	}
	name := e.TXT["name"]
	if name == "" {
		name = e.Instance
	}
	d.peers[e.Instance] = peer{ID: id, Name: name, Platform: e.TXT["plat"], Host: host, Port: port}
}

// rename re-advertises under a new display name.
func (d *discovery) rename(name string) {
	select {
	case d.name <- name:
	default:
		// A pending rename is replaced by the newest.
		select {
		case <-d.name:
		default:
		}
		d.name <- name
	}
}

// startDiscovery runs the advertise and browse loops until ctx ends. Both
// retry after an error, so a box without multicast just logs once a minute.
func (a *agent) startDiscovery(ctx context.Context) {
	go a.advertiseLoop(ctx)
	go a.browseLoop(ctx)
}

func (a *agent) advertiseLoop(ctx context.Context) {
	name := a.cfg.Snapshot().Name
	for ctx.Err() == nil {
		txt := map[string]string{"v": "1", "id": a.id, "name": name, "plat": a.backend.Platform(), "port": strconv.Itoa(a.port)}
		stop, err := mdnsdisc.Advertise(ctx, serviceType, name, a.port, txt)
		if err != nil {
			a.logf("mdns: %v (retrying in %s)", err, retryDiscovery)
			select {
			case <-ctx.Done():
				return
			case name = <-a.disc.name:
			case <-time.After(retryDiscovery):
			}
			continue
		}
		a.debugf("mdns: advertising %q on port %d", name, a.port)
		select {
		case <-ctx.Done():
			stop()
			return
		case name = <-a.disc.name:
			stop()
		}
	}
}

func (a *agent) browseLoop(ctx context.Context) {
	for ctx.Err() == nil {
		err := mdnsdisc.Browse(ctx, serviceType, a.disc.handle)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.debugf("mdns browse: %v (retrying in %s)", err, retryDiscovery)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDiscovery):
		}
	}
}

// lanURLs lists http://<ip>:<port>/ for every non-loopback IPv4 address of
// an interface that is up, which is what the settings page shows and
// encodes in the QR code.
func lanURLs(port int) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var urls []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipn, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			v4 := ipn.IP.To4()
			if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
				continue
			}
			urls = append(urls, "http://"+v4.String()+":"+strconv.Itoa(port)+"/")
		}
	}
	return urls
}
