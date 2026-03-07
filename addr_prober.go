package main

import (
	"context"
	"sync"
	"time"

	"github.com/ipfs/boxo/routing/http/types"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	probeAddrAttemptsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "probe_addr_attempts",
		Namespace: name,
		Subsystem: "addr_prober",
		Help:      "Number of per-addr probe attempts by result (alive/dead)",
	}, []string{"result"})

	probeAddrDurationHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:      "probe_addr_duration_seconds",
		Namespace: name,
		Subsystem: "addr_prober",
		Help:      "Duration of individual addr probe operations in seconds",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2, 3, 5},
	})
)

// addrProber probes individual multiaddrs using a pool of ephemeral libp2p
// hosts. Each host has its own peerstore; by adding a single addr to an
// empty peerstore before calling host.Connect, we ensure only that addr
// is dialed (same technique as vole's identify command). A full libp2p
// handshake proves the peer is real, not just an open port.
//
// The pool (buffered channel) allows concurrent probes for the same peer:
// each goroutine takes a host, probes one addr, and returns the host.
// With pool size 10 and 20 probe targets, probing completes in ~2 rounds.
// Alive ports respond in <500ms; dead ports hit the timeout (default 5s).
//
// This is called from probeFilterIter.dispatchProbe, which runs in
// background goroutines so the streaming response is not blocked.
type addrProber struct {
	hostPool chan host.Host // buffered channel used as a pool
	timeout  time.Duration
}

func newAddrProber(poolSize int, timeout time.Duration) (*addrProber, error) {
	pool := make(chan host.Host, poolSize)
	for range poolSize {
		h, err := libp2p.New(libp2p.NoListenAddrs)
		if err != nil {
			// close any hosts already created
			close(pool)
			for h := range pool {
				h.Close()
			}
			return nil, err
		}
		pool <- h
	}
	return &addrProber{
		hostPool: pool,
		timeout:  timeout,
	}, nil
}

// probeAddr tests whether a single multiaddr is reachable for the given peer
// using a full libp2p handshake via an ephemeral host from the pool.
func (p *addrProber) probeAddr(ctx context.Context, pid peer.ID, addr ma.Multiaddr) bool {
	// take a host from the pool
	var h host.Host
	select {
	case h = <-p.hostPool:
	case <-ctx.Done():
		return false
	}
	defer func() { p.hostPool <- h }()

	// clear any previous state for this peer
	h.Peerstore().ClearAddrs(pid)
	h.Peerstore().AddAddr(pid, addr, time.Minute)

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	start := time.Now()
	err := h.Connect(ctx, peer.AddrInfo{ID: pid, Addrs: []ma.Multiaddr{addr}})
	probeAddrDurationHistogram.Observe(time.Since(start).Seconds())

	if err != nil {
		probeAddrAttemptsCounter.WithLabelValues("dead").Inc()
		return false
	}

	// close connection after successful probe
	_ = h.Network().ClosePeer(pid)
	probeAddrAttemptsCounter.WithLabelValues("alive").Inc()
	return true
}

// addrGroup represents a set of multiaddrs that share the same (IP, port, L4) key.
// We only probe one representative addr per group and apply the result to all.
type addrGroup struct {
	representative ma.Multiaddr // shortest/simplest addr in the group
	indices        []int        // indices into the original addrs slice
}

// probeAddrs probes addrs for a peer, deduplicating by (IP, port, L4).
//
// Multiple multiaddrs can share the same underlying port, e.g.
// /ip4/x/udp/4001/quic-v1 and /ip4/x/udp/4001/quic-v1/webtransport
// both use UDP:4001. We group by (IP, port, L4), probe once per group
// using the shortest multiaddr as representative, and apply the result
// to all members of the group.
//
// Addrs that can't be probed (relay, HTTP, DNS) are kept unchanged.
// If ALL probes fail, the peer is likely offline and we return all addrs
// unchanged (fail-open) so downstream clients can still attempt connection.
func (p *addrProber) probeAddrs(ctx context.Context, pid peer.ID, addrs []types.Multiaddr) []types.Multiaddr {
	if len(addrs) == 0 {
		return addrs
	}

	// group addrs by (IP, port, L4) for deduplication
	type groupKey struct {
		addrTransportKey
		port int
	}

	groups := make(map[groupKey]*addrGroup)
	var unprobable []int // indices of addrs we can't/shouldn't probe

	for i, addr := range addrs {
		key, port, ok := extractAddrTransportKey(addr.Multiaddr)
		if !ok {
			unprobable = append(unprobable, i)
			continue
		}

		gk := groupKey{key, port}
		g, exists := groups[gk]
		if !exists {
			g = &addrGroup{
				representative: addr.Multiaddr,
			}
			groups[gk] = g
		}
		g.indices = append(g.indices, i)

		// prefer shorter multiaddr as representative (simpler protocol stack)
		if len(addr.Multiaddr.Bytes()) < len(g.representative.Bytes()) {
			g.representative = addr.Multiaddr
		}
	}

	if len(groups) == 0 {
		return addrs
	}

	// probe each unique (IP, port, L4) concurrently
	type probeResult struct {
		gk    groupKey
		alive bool
	}

	results := make(chan probeResult, len(groups))
	var wg sync.WaitGroup

	for gk, g := range groups {
		wg.Go(func() {
			alive := p.probeAddr(ctx, pid, g.representative)
			results <- probeResult{gk: gk, alive: alive}
		})
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	aliveGroups := make(map[groupKey]bool, len(groups))
	aliveCount := 0
	for r := range results {
		aliveGroups[r.gk] = r.alive
		if r.alive {
			aliveCount++
		}
	}

	// fail-open: if all probes failed, peer is likely offline; return all addrs unchanged
	if aliveCount == 0 {
		logger.Debugw("all probes failed, returning all addrs (fail-open)",
			"peer", pid, "probed", len(groups))
		return addrs
	}

	// build result: keep alive addrs + unprobable addrs, drop dead
	result := make([]types.Multiaddr, 0, len(addrs))
	for _, i := range unprobable {
		result = append(result, addrs[i])
	}

	var filtered int
	for gk, g := range groups {
		if aliveGroups[gk] {
			for _, i := range g.indices {
				result = append(result, addrs[i])
			}
		} else {
			filtered += len(g.indices)
		}
	}

	if filtered > 0 {
		logger.Debugw("probed and filtered dead addrs",
			"peer", pid, "alive", aliveCount, "dead", len(groups)-aliveCount, "filtered_addrs", filtered)
		staleAddrsFilteredCounter.Add(float64(filtered))
	}

	return result
}

func (p *addrProber) close() {
	close(p.hostPool)
	for h := range p.hostPool {
		h.Close()
	}
}
