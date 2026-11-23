package engine

import (
	"context"
	"errors"
	"net"
	"sort"
	"strconv"
	"time"

	"faro/internal/clashapi"
)

// Two things are asked of a node, and shown apart. How far away it is: the time
// for a TCP connection to its server, the round trip, which is what a ping
// says and what other clients show as a node's delay. And whether it works:
// whether a request goes out through it, and comes back, which a server that
// answers a connection does not tell. The first is measured from here; the
// second goes through the core.

// nodeAddr is where a node's server is.
type nodeAddr struct {
	host string
	port int
	udp  bool // a protocol over UDP: there is no TCP connection to time
}

// addrOf reads a node's server out of its outbound.
func addrOf(kind string, out map[string]any) nodeAddr {
	a := nodeAddr{}
	switch kind {
	case "hysteria", "hysteria2", "tuic", "wireguard":
		a.udp = true
	}
	a.host, _ = out["server"].(string)
	switch v := out["server_port"].(type) {
	case int:
		a.port = v
	case float64:
		a.port = int(v)
	}
	return a
}

// rttOf is the round trip to a node's server, in ms: through the hook in tests,
// else a TCP connection to it.
func (e *Engine) rttOf(ctx context.Context, tag string) (int, error) {
	if e.opts.RTT != nil {
		return e.opts.RTT(ctx, tag)
	}
	e.mu.Lock()
	a, ok := e.addrs[tag]
	e.mu.Unlock()
	if !ok || a.udp || a.host == "" || a.port == 0 {
		return 0, errors.New("no TCP connection to time")
	}
	return dialRTT(ctx, a.host, a.port)
}

// measure asks both questions of a node at once. When the server cannot be timed
// from here (a protocol over UDP, a name only the core can resolve) but a request
// went through, the time that took stands in for the round trip, so that a node
// that works is never shown as having no answer.
func (e *Engine) measure(ctx context.Context, api *clashapi.Client, tag string) (rtt int, works bool) {
	type answer struct {
		ms  int
		err error
	}
	rc, pc := make(chan answer, 1), make(chan answer, 1)
	go func() { ms, err := e.rttOf(ctx, tag); rc <- answer{ms, err} }()
	go func() { ms, err := e.probe(ctx, api, tag); pc <- answer{ms, err} }()
	r, p := <-rc, <-pc
	works = p.err == nil && p.ms > 0
	switch {
	case r.err == nil && r.ms > 0:
		rtt = r.ms
	case works:
		rtt = p.ms
	default:
		rtt = -1
	}
	return rtt, works
}

// dialRTT times TCP connections to host:port and gives the middle one: the name
// is looked up first, so that only the connection is timed. A server that does
// not answer the first is not asked again.
func dialRTT(ctx context.Context, host string, port int) (int, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addrs) == 0 {
			return 0, errors.New("cannot find " + host)
		}
		ip = addrs[0].IP
		for _, a := range addrs { // an IPv4 address first: that is what a node is dialled on
			if a.IP.To4() != nil {
				ip = a.IP
				break
			}
		}
	}
	target := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	var times []time.Duration
	for i := 0; i < 3; i++ {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		t := time.Now()
		c, err := (&net.Dialer{}).DialContext(cctx, "tcp", target)
		d := time.Since(t)
		cancel()
		if err != nil {
			if i == 0 {
				return 0, err
			}
			continue
		}
		c.Close()
		times = append(times, d)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	ms := int((times[len(times)/2] + time.Millisecond - 1) / time.Millisecond) // rounded up: never 0
	return max(ms, 1), nil
}
