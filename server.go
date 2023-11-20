package main

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/ipfs/boxo/routing/http/client"
	"github.com/ipfs/boxo/routing/http/server"
<<<<<<< HEAD
	logging "github.com/ipfs/go-log"
||||||| parent of 3a9d78c (feat: proxy all delegated routing)
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
=======
>>>>>>> 3a9d78c (feat: proxy all delegated routing)
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/routing"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
)

var logger = logging.Logger("someguy")

func start(ctx context.Context, port int, runAcceleratedDHTClient bool, contentEndpoints, peerEndpoints, ipnsEndpoints []string) error {
	h, err := newHost(runAcceleratedDHTClient)
	if err != nil {
		return err
	}

	var dhtRouting routing.Routing
	if runAcceleratedDHTClient {
		wrappedDHT, err := newBundledDHT(ctx, h)
		if err != nil {
			return err
		}
		dhtRouting = wrappedDHT
	} else {
		standardDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeClient), dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
		if err != nil {
			return err
		}
		dhtRouting = standardDHT
	}

	crRouters, err := getCombinedRouting(contentEndpoints, dhtRouting)
	if err != nil {
		return err
	}

	prRouters, err := getCombinedRouting(peerEndpoints, dhtRouting)
	if err != nil {
		return err
	}

	ipnsRouters, err := getCombinedRouting(ipnsEndpoints, dhtRouting)
	if err != nil {
		return err
	}

	log.Printf("Listening on http://0.0.0.0:%d", port)
	log.Printf("Delegated Routing API on http://127.0.0.1:%d/routing/v1", port)

	http.Handle("/", server.Handler(&composableRouter{
		providers: crRouters,
		peers:     prRouters,
		ipns:      ipnsRouters,
	}))
	return http.ListenAndServe(":"+strconv.Itoa(port), nil)
}

func newHost(highOutboundLimits bool) (host.Host, error) {
	if !highOutboundLimits {
		return libp2p.New()
	}

	defaultLimits := rcmgr.DefaultLimits
	libp2p.SetDefaultServiceLimits(&defaultLimits)
	// Outbound conns and FDs are set very high to allow for the accelerated DHT client to (re)load its routing table.
	// Currently it doesn't gracefully handle RM throttling--once it does we can lower these.
	// High outbound conn limits are considered less of a DoS risk than high inbound conn limits.
	// Also note that, due to the behavior of the accelerated DHT client, we don't need many streams, just conns.
	if minOutbound := 65536; defaultLimits.SystemBaseLimit.ConnsOutbound < minOutbound {
		defaultLimits.SystemBaseLimit.ConnsOutbound = minOutbound
		if defaultLimits.SystemBaseLimit.Conns < defaultLimits.SystemBaseLimit.ConnsOutbound {
			defaultLimits.SystemBaseLimit.Conns = defaultLimits.SystemBaseLimit.ConnsOutbound
		}
	}
	if minFD := 4096; defaultLimits.SystemBaseLimit.FD < minFD {
		defaultLimits.SystemBaseLimit.FD = minFD
	}
	defaultLimitConfig := defaultLimits.AutoScale()

	rm, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(defaultLimitConfig))
	if err != nil {
		return nil, err
	}
	h, err := libp2p.New(libp2p.ResourceManager(rm))
	if err != nil {
		return nil, err
	}

	return h, nil
}

func getCombinedRouting(endpoints []string, dht routing.Routing) (router, error) {
	if len(endpoints) == 0 {
		return libp2pRouter{routing: dht}, nil
	}

	var routers []router

	for _, endpoint := range endpoints {
		drclient, err := client.New(endpoint)
		if err != nil {
			return nil, err
		}
		routers = append(routers, clientRouter{Client: drclient})
	}

	return parallelRouter{
		routers: append(routers, libp2pRouter{routing: dht}),
	}, nil
}
