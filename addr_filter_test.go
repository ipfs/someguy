package main

import (
	"testing"

	"github.com/ipfs/boxo/routing/http/types"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustMA(t *testing.T, s string) ma.Multiaddr {
	t.Helper()
	addr, err := ma.NewMultiaddr(s)
	require.NoError(t, err)
	return addr
}

func toTypesAddrs(t *testing.T, strs ...string) []types.Multiaddr {
	t.Helper()
	result := make([]types.Multiaddr, len(strs))
	for i, s := range strs {
		result[i] = types.Multiaddr{Multiaddr: mustMA(t, s)}
	}
	return result
}

func typesAddrStrings(addrs []types.Multiaddr) []string {
	out := make([]string, len(addrs))
	for i, a := range addrs {
		out[i] = a.Multiaddr.String()
	}
	return out
}

func maStrings(addrs []ma.Multiaddr) []string {
	out := make([]string, len(addrs))
	for i, a := range addrs {
		out[i] = a.String()
	}
	return out
}

func TestExtractAddrTransportKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		addr     string
		wantKey  addrTransportKey
		wantPort int
		wantOK   bool
	}{
		{
			name:     "ip4 tcp",
			addr:     "/ip4/209.222.4.177/tcp/4001",
			wantKey:  addrTransportKey{ip: "209.222.4.177", l4Code: ma.P_TCP},
			wantPort: 4001,
			wantOK:   true,
		},
		{
			name:     "ip4 udp quic-v1",
			addr:     "/ip4/209.222.4.177/udp/4001/quic-v1",
			wantKey:  addrTransportKey{ip: "209.222.4.177", l4Code: ma.P_UDP},
			wantPort: 4001,
			wantOK:   true,
		},
		{
			name:     "ip4 udp quic-v1 webtransport",
			addr:     "/ip4/209.222.4.177/udp/4001/quic-v1/webtransport",
			wantKey:  addrTransportKey{ip: "209.222.4.177", l4Code: ma.P_UDP},
			wantPort: 4001,
			wantOK:   true,
		},
		{
			name:     "ip6 tcp",
			addr:     "/ip6/2001:db8::1/tcp/4001",
			wantKey:  addrTransportKey{ip: "2001:db8::1", l4Code: ma.P_TCP},
			wantPort: 4001,
			wantOK:   true,
		},
		{
			name:     "ip6 udp quic-v1",
			addr:     "/ip6/2604:1380:4601:f600::5/udp/4001/quic-v1",
			wantKey:  addrTransportKey{ip: "2604:1380:4601:f600::5", l4Code: ma.P_UDP},
			wantPort: 4001,
			wantOK:   true,
		},
		{
			name:   "circuit relay addr is skipped",
			addr:   "/ip4/1.2.3.4/tcp/4001/p2p/12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj/p2p-circuit",
			wantOK: false,
		},
		{
			name:   "dns addr without IP is skipped",
			addr:   "/dns4/example.com/tcp/443",
			wantOK: false,
		},
		{
			name:   "http addr is skipped",
			addr:   "/ip4/209.222.4.177/tcp/443/tls/http",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := mustMA(t, tt.addr)
			key, port, ok := extractAddrTransportKey(addr)
			assert.Equal(t, tt.wantOK, ok, "ok mismatch")
			if ok {
				assert.Equal(t, tt.wantKey, key, "key mismatch")
				assert.Equal(t, tt.wantPort, port, "port mismatch")
			}
		})
	}
}

func TestFilterStalePortAddrs(t *testing.T) {
	t.Parallel()

	t.Run("filters stale UDP ports on same IP", func(t *testing.T) {
		addrs := toTypesAddrs(t,
			"/ip4/209.222.4.177/udp/4001/quic-v1",
			"/ip4/209.222.4.177/udp/1282/quic-v1",
			"/ip4/209.222.4.177/udp/61078/quic-v1",
			"/ip4/209.222.4.177/udp/4001/quic-v1/webtransport",
			"/ip4/209.222.4.177/udp/1282/quic-v1/webtransport",
		)
		connectedAddr := mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1")
		result := filterStalePortAddrs(addrs, connectedAddr)

		got := typesAddrStrings(result)
		assert.ElementsMatch(t, []string{
			"/ip4/209.222.4.177/udp/4001/quic-v1",
			"/ip4/209.222.4.177/udp/4001/quic-v1/webtransport",
		}, got)
	})

	t.Run("does not touch TCP when connected via UDP", func(t *testing.T) {
		addrs := toTypesAddrs(t,
			"/ip4/209.222.4.177/tcp/4001",
			"/ip4/209.222.4.177/tcp/61078",
			"/ip4/209.222.4.177/udp/4001/quic-v1",
			"/ip4/209.222.4.177/udp/1282/quic-v1",
		)
		connectedAddr := mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1")
		result := filterStalePortAddrs(addrs, connectedAddr)

		got := typesAddrStrings(result)
		assert.ElementsMatch(t, []string{
			"/ip4/209.222.4.177/tcp/4001",
			"/ip4/209.222.4.177/tcp/61078",
			"/ip4/209.222.4.177/udp/4001/quic-v1",
		}, got)
	})

	t.Run("does not touch different IPs", func(t *testing.T) {
		addrs := toTypesAddrs(t,
			"/ip4/209.222.4.177/udp/4001/quic-v1",
			"/ip4/209.222.4.177/udp/1282/quic-v1",
			"/ip4/10.0.0.1/udp/5555/quic-v1",
			"/ip6/2001:db8::1/udp/4001/quic-v1",
		)
		connectedAddr := mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1")
		result := filterStalePortAddrs(addrs, connectedAddr)

		got := typesAddrStrings(result)
		assert.ElementsMatch(t, []string{
			"/ip4/209.222.4.177/udp/4001/quic-v1",
			"/ip4/10.0.0.1/udp/5555/quic-v1",
			"/ip6/2001:db8::1/udp/4001/quic-v1",
		}, got)
	})

	t.Run("keeps relay addrs unchanged", func(t *testing.T) {
		addrs := toTypesAddrs(t,
			"/ip4/209.222.4.177/udp/4001/quic-v1",
			"/ip4/209.222.4.177/udp/1282/quic-v1",
			"/ip4/1.2.3.4/tcp/4001/p2p/12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj/p2p-circuit",
		)
		connectedAddr := mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1")
		result := filterStalePortAddrs(addrs, connectedAddr)

		got := typesAddrStrings(result)
		assert.ElementsMatch(t, []string{
			"/ip4/209.222.4.177/udp/4001/quic-v1",
			"/ip4/1.2.3.4/tcp/4001/p2p/12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj/p2p-circuit",
		}, got)
	})

	t.Run("no-op when connectedAddr is nil", func(t *testing.T) {
		addrs := toTypesAddrs(t,
			"/ip4/209.222.4.177/udp/4001/quic-v1",
			"/ip4/209.222.4.177/udp/1282/quic-v1",
		)
		result := filterStalePortAddrs(addrs, nil)
		assert.Equal(t, addrs, result)
	})

	t.Run("no-op when empty addrs", func(t *testing.T) {
		connectedAddr := mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1")
		result := filterStalePortAddrs(nil, connectedAddr)
		assert.Nil(t, result)
	})
}

func TestNeedsProbing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		addrs []string
		want  bool
	}{
		{
			name: "multi-port on same IP and L4 triggers probing",
			addrs: []string{
				"/ip4/209.222.4.177/udp/4001/quic-v1",
				"/ip4/209.222.4.177/udp/1282/quic-v1",
			},
			want: true,
		},
		{
			name: "2 v4 IPs does not trigger probing (normal multi-homed)",
			addrs: []string{
				"/ip4/209.222.4.177/tcp/4001",
				"/ip4/1.2.3.4/tcp/4001",
			},
			want: false,
		},
		{
			name: "3 v4 IPs does not trigger probing",
			addrs: []string{
				"/ip4/209.222.4.177/tcp/4001",
				"/ip4/1.2.3.4/tcp/4001",
				"/ip4/10.20.30.40/tcp/4001",
			},
			want: false,
		},
		{
			name: "4 v4 IPs triggers probing",
			addrs: []string{
				"/ip4/209.222.4.177/tcp/4001",
				"/ip4/1.2.3.4/tcp/4001",
				"/ip4/10.20.30.40/tcp/4001",
				"/ip4/50.60.70.80/tcp/4001",
			},
			want: true,
		},
		{
			name: "4 v6 IPs triggers probing",
			addrs: []string{
				"/ip6/2001:db8::1/tcp/4001",
				"/ip6/2001:db8::2/tcp/4001",
				"/ip6/2001:db8::3/tcp/4001",
				"/ip6/2001:db8::4/tcp/4001",
			},
			want: true,
		},
		{
			name: "single port and IP does not trigger probing",
			addrs: []string{
				"/ip4/209.222.4.177/udp/4001/quic-v1",
				"/ip4/209.222.4.177/udp/4001/quic-v1/webtransport",
				"/ip4/209.222.4.177/tcp/4001",
			},
			want: false,
		},
		{
			name: "dual-stack same port does not trigger probing",
			addrs: []string{
				"/ip4/209.222.4.177/tcp/4001",
				"/ip6/2001:db8::1/tcp/4001",
			},
			want: false,
		},
		{
			name: "http addrs are ignored for probing decision",
			addrs: []string{
				"/ip4/209.222.4.177/tcp/443/tls/http",
				"/ip4/209.222.4.177/tcp/4001",
			},
			want: false,
		},
		{
			name:  "empty addrs",
			addrs: nil,
			want:  false,
		},
		{
			name: "relay-only addrs do not trigger probing",
			addrs: []string{
				"/ip4/1.2.3.4/tcp/4001/p2p/12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj/p2p-circuit",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addrs := toTypesAddrs(t, tt.addrs...)
			got := needsProbing(addrs)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFindStalePortAddrs(t *testing.T) {
	t.Parallel()

	t.Run("finds stale addrs on same IP and L4", func(t *testing.T) {
		allAddrs := []ma.Multiaddr{
			mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1"),
			mustMA(t, "/ip4/209.222.4.177/udp/1282/quic-v1"),
			mustMA(t, "/ip4/209.222.4.177/udp/61078/quic-v1"),
			mustMA(t, "/ip4/209.222.4.177/tcp/4001"),
		}
		connectedAddr := mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1")
		stale := findStalePortAddrs(allAddrs, connectedAddr)

		got := maStrings(stale)
		assert.ElementsMatch(t, []string{
			"/ip4/209.222.4.177/udp/1282/quic-v1",
			"/ip4/209.222.4.177/udp/61078/quic-v1",
		}, got)
	})

	t.Run("returns nil when no stale addrs", func(t *testing.T) {
		allAddrs := []ma.Multiaddr{
			mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1"),
			mustMA(t, "/ip4/209.222.4.177/tcp/4001"),
		}
		connectedAddr := mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1")
		stale := findStalePortAddrs(allAddrs, connectedAddr)
		assert.Nil(t, stale)
	})

	t.Run("returns nil when connectedAddr is nil", func(t *testing.T) {
		allAddrs := []ma.Multiaddr{
			mustMA(t, "/ip4/209.222.4.177/udp/4001/quic-v1"),
		}
		stale := findStalePortAddrs(allAddrs, nil)
		assert.Nil(t, stale)
	})
}
