// addr_filter.go provides passive stale-address filtering and detection
// heuristics for the active probing layer (see addr_prober.go).
//
// Problem: some DHT server implementations never expire old observed
// addresses for a peer. Peers with dynamic ports (e.g. UPnP on consumer
// routers) or changing IPs (roaming, ISP changes) accumulate dead addresses
// over time. A provider record with 60 dead port addresses before the one
// that works makes the peer effectively unreachable.
//
// Passive filtering (filterStalePortAddrs): when someguy has previously
// connected to a peer, it remembers the working address. On subsequent
// lookups, addresses on the same IP and layer-4 protocol but with a
// different port are stripped out. This is fast and runs inline.
//
// Detection (needsProbing): when no known-good address exists (first
// encounter), this heuristic checks whether the address set looks
// suspicious -- multiple ports on the same (IP, L4), or multiple IPs
// within the same address family. If so, the record is handed to the
// async probing layer (probeFilterIter in server_routers.go).
package main

import (
	"strconv"

	"github.com/ipfs/boxo/routing/http/types"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var staleAddrsFilteredCounter = promauto.NewCounter(prometheus.CounterOpts{
	Name:      "stale_addrs_filtered",
	Namespace: name,
	Subsystem: "addr_filter",
	Help:      "Number of stale addresses filtered from responses (same IP, different port from last known-good connection)",
})

// addrTransportKey groups multiaddrs by IP address and layer-4 protocol.
// Multiaddrs sharing the same key but differing only in port are
// candidates for stale address filtering.
type addrTransportKey struct {
	ip     string // e.g. "209.222.4.177" or "2001:db8::1"
	l4Code int    // ma.P_TCP or ma.P_UDP
}

// extractAddrTransportKey returns the IP, layer-4 protocol, and port from a
// multiaddr. Returns false for relay (circuit), HTTP, and DNS addresses, or
// multiaddrs without a standard IP + transport structure.
func extractAddrTransportKey(addr ma.Multiaddr) (key addrTransportKey, port int, ok bool) {
	// skip relay addresses: the IP/port belongs to the relay, not the peer
	if _, err := addr.ValueForProtocol(ma.P_CIRCUIT); err == nil {
		return addrTransportKey{}, 0, false
	}

	// skip HTTP addresses: trustless gateway, not a libp2p peer
	if _, err := addr.ValueForProtocol(ma.P_HTTP); err == nil {
		return addrTransportKey{}, 0, false
	}

	if v, err := addr.ValueForProtocol(ma.P_IP4); err == nil {
		key.ip = v
	} else if v, err := addr.ValueForProtocol(ma.P_IP6); err == nil {
		key.ip = v
	} else {
		return addrTransportKey{}, 0, false
	}

	if v, err := addr.ValueForProtocol(ma.P_TCP); err == nil {
		key.l4Code = ma.P_TCP
		port, _ = strconv.Atoi(v)
		ok = true
	} else if v, err := addr.ValueForProtocol(ma.P_UDP); err == nil {
		key.l4Code = ma.P_UDP
		port, _ = strconv.Atoi(v)
		ok = true
	}
	return
}

// filterStalePortAddrs removes multiaddrs that share the same (IP, layer-4
// protocol) as connectedAddr but have a different port. These are likely
// stale port forwards from old NAT mappings.
//
// Addrs on different IPs, different L4 protocols, or unparseable addrs
// are kept unchanged.
func filterStalePortAddrs(addrs []types.Multiaddr, connectedAddr ma.Multiaddr) []types.Multiaddr {
	if connectedAddr == nil || len(addrs) == 0 {
		return addrs
	}

	goodKey, goodPort, ok := extractAddrTransportKey(connectedAddr)
	if !ok {
		return addrs
	}

	result := make([]types.Multiaddr, 0, len(addrs))
	var filtered int

	for _, addr := range addrs {
		key, port, ok := extractAddrTransportKey(addr.Multiaddr)
		if !ok || key != goodKey {
			result = append(result, addr)
			continue
		}
		if port == goodPort {
			result = append(result, addr)
		} else {
			filtered++
		}
	}

	if filtered > 0 {
		staleAddrsFilteredCounter.Add(float64(filtered))
	}
	return result
}

// needsProbing returns true when the addr set shows signs of stale addresses:
// - multi-port: any (IP, L4) group has more than one distinct port
// - multi-IP: any address family (v4 or v6) has more than one distinct IP
func needsProbing(addrs []types.Multiaddr) bool {
	type ipL4 struct {
		ip     string
		l4Code int
	}

	ports := make(map[ipL4]map[int]struct{})
	v4IPs := make(map[string]struct{})
	v6IPs := make(map[string]struct{})

	for _, addr := range addrs {
		key, port, ok := extractAddrTransportKey(addr.Multiaddr)
		if !ok {
			continue
		}

		k := ipL4(key)
		if ports[k] == nil {
			ports[k] = make(map[int]struct{})
		}
		ports[k][port] = struct{}{}

		// track distinct IPs per address family
		if _, err := addr.Multiaddr.ValueForProtocol(ma.P_IP4); err == nil {
			v4IPs[key.ip] = struct{}{}
		} else if _, err := addr.Multiaddr.ValueForProtocol(ma.P_IP6); err == nil {
			v6IPs[key.ip] = struct{}{}
		}
	}

	// multi-port: any (IP, L4) has >1 port
	for _, ps := range ports {
		if len(ps) > 1 {
			return true
		}
	}

	// multi-IP: same address family has many distinct IPs.
	// 2-3 IPs is normal (dual WAN, cloud instances with public + VPC),
	// but 4+ within a single family suggests stale addrs from ISP/roaming changes not being expired by some poorly written third-party DHT peers.
	if len(v4IPs) > 3 || len(v6IPs) > 3 {
		return true
	}

	return false
}

// findStalePortAddrs returns multiaddrs from allAddrs that share the same
// (IP, layer-4 protocol) as connectedAddr but have a different port.
// Used for cleaning up stale entries from the addr book cache.
func findStalePortAddrs(allAddrs []ma.Multiaddr, connectedAddr ma.Multiaddr) []ma.Multiaddr {
	if connectedAddr == nil || len(allAddrs) == 0 {
		return nil
	}

	goodKey, goodPort, ok := extractAddrTransportKey(connectedAddr)
	if !ok {
		return nil
	}

	var stale []ma.Multiaddr
	for _, addr := range allAddrs {
		key, port, ok := extractAddrTransportKey(addr)
		if !ok || key != goodKey {
			continue
		}
		if port != goodPort {
			stale = append(stale, addr)
		}
	}
	return stale
}
