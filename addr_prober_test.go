package main

import (
	"context"
	"testing"
	"time"

	"github.com/ipfs/boxo/routing/http/types"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeAddr(t *testing.T) {
	t.Parallel()

	// create a target libp2p host that listens on a local TCP port
	target, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer target.Close()

	targetAddr := target.Addrs()[0]
	targetID := target.ID()

	prober, err := newAddrProber(2, 5*time.Second)
	require.NoError(t, err)
	defer prober.close()

	t.Run("probe succeeds for reachable addr", func(t *testing.T) {
		ctx := t.Context()
		result := prober.probeAddr(ctx, targetID, targetAddr)
		assert.True(t, result, "probe should succeed for reachable addr")
	})

	t.Run("probe fails for unreachable addr", func(t *testing.T) {
		ctx := t.Context()
		// use a port that nothing is listening on
		deadAddr := mustMA(t, "/ip4/127.0.0.1/tcp/1")
		result := prober.probeAddr(ctx, targetID, deadAddr)
		assert.False(t, result, "probe should fail for unreachable addr")
	})
}

func TestProbeAddrs(t *testing.T) {
	t.Parallel()

	// create two target hosts: one reachable, one we'll stop
	alive, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer alive.Close()

	dead, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	deadAddr := dead.Addrs()[0]
	deadID := dead.ID()
	dead.Close() // stop listening to make it unreachable

	prober, err := newAddrProber(3, 3*time.Second)
	require.NoError(t, err)
	defer prober.close()

	t.Run("filters dead addrs, keeps alive", func(t *testing.T) {
		ctx := t.Context()
		aliveAddr := alive.Addrs()[0]

		addrs := []types.Multiaddr{
			{Multiaddr: aliveAddr},
			{Multiaddr: mustMA(t, "/ip4/127.0.0.1/tcp/1")}, // dead port
		}

		result := prober.probeAddrs(ctx, alive.ID(), addrs)
		resultStrs := typesAddrStrings(result)
		assert.Contains(t, resultStrs, aliveAddr.String())
		assert.NotContains(t, resultStrs, "/ip4/127.0.0.1/tcp/1")
	})

	t.Run("fail-open when all probes fail", func(t *testing.T) {
		ctx := t.Context()
		addrs := []types.Multiaddr{
			{Multiaddr: deadAddr},
			{Multiaddr: mustMA(t, "/ip4/127.0.0.1/tcp/1")},
		}

		result := prober.probeAddrs(ctx, deadID, addrs)
		// should return all addrs unchanged
		assert.Len(t, result, len(addrs))
	})

	t.Run("keeps unprobable addrs (relay, http)", func(t *testing.T) {
		ctx := t.Context()
		aliveAddr := alive.Addrs()[0]
		relayAddr := mustMA(t, "/ip4/1.2.3.4/tcp/4001/p2p/12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj/p2p-circuit")
		httpAddr := mustMA(t, "/ip4/1.2.3.4/tcp/443/tls/http")

		addrs := []types.Multiaddr{
			{Multiaddr: aliveAddr},
			{Multiaddr: mustMA(t, "/ip4/127.0.0.1/tcp/1")}, // dead
			{Multiaddr: relayAddr},
			{Multiaddr: httpAddr},
		}

		result := prober.probeAddrs(ctx, alive.ID(), addrs)
		resultStrs := typesAddrStrings(result)
		assert.Contains(t, resultStrs, aliveAddr.String(), "alive addr should be kept")
		assert.Contains(t, resultStrs, relayAddr.String(), "relay addr should be kept")
		assert.Contains(t, resultStrs, httpAddr.String(), "http addr should be kept")
		assert.NotContains(t, resultStrs, "/ip4/127.0.0.1/tcp/1", "dead addr should be filtered")
	})

	t.Run("deduplicates by (IP, port, L4)", func(t *testing.T) {
		ctx := t.Context()
		aliveAddr := alive.Addrs()[0]

		// two addrs sharing the same TCP port: one bare, one with /p2p/ suffix
		addr1 := aliveAddr
		addr2, _ := ma.NewMultiaddr(aliveAddr.String() + "/p2p/" + alive.ID().String())

		addrs := []types.Multiaddr{
			{Multiaddr: addr1},
			{Multiaddr: addr2},
		}

		result := prober.probeAddrs(ctx, alive.ID(), addrs)
		// both should be kept since they share the same alive (IP, port, L4)
		assert.Len(t, result, 2)
	})
}

func TestProbeAddrsConcurrency(t *testing.T) {
	t.Parallel()

	// create target
	target, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer target.Close()

	// small pool to test contention
	prober, err := newAddrProber(2, 3*time.Second)
	require.NoError(t, err)
	defer prober.close()

	ctx := t.Context()
	targetAddr := target.Addrs()[0]

	// create multiple distinct addrs to probe (each unique port)
	addrs := []types.Multiaddr{
		{Multiaddr: targetAddr},
		{Multiaddr: mustMA(t, "/ip4/127.0.0.1/tcp/1")},
		{Multiaddr: mustMA(t, "/ip4/127.0.0.1/tcp/2")},
		{Multiaddr: mustMA(t, "/ip4/127.0.0.1/tcp/3")},
	}

	result := prober.probeAddrs(ctx, target.ID(), addrs)
	resultStrs := typesAddrStrings(result)
	assert.Contains(t, resultStrs, targetAddr.String(), "alive addr should survive")
	assert.NotContains(t, resultStrs, "/ip4/127.0.0.1/tcp/1")
	assert.NotContains(t, resultStrs, "/ip4/127.0.0.1/tcp/2")
	assert.NotContains(t, resultStrs, "/ip4/127.0.0.1/tcp/3")
}

func TestProbeAddrsContextCancellation(t *testing.T) {
	t.Parallel()

	prober, err := newAddrProber(2, 5*time.Second)
	require.NoError(t, err)
	defer prober.close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	pid := peer.ID("test-peer")
	addrs := []types.Multiaddr{
		{Multiaddr: mustMA(t, "/ip4/127.0.0.1/tcp/1")},
		{Multiaddr: mustMA(t, "/ip4/127.0.0.1/tcp/2")},
	}

	// should return all addrs (fail-open) since probes can't run
	result := prober.probeAddrs(ctx, pid, addrs)
	assert.Len(t, result, len(addrs))
}
