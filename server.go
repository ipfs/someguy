package main

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/CAFxX/httpcompression"
	"github.com/felixge/httpsnoop"
	"github.com/ipfs/boxo/routing/http/client"
	"github.com/ipfs/boxo/routing/http/server"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/routing"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	metrics "github.com/slok/go-http-metrics/metrics/prometheus"
	"github.com/slok/go-http-metrics/middleware"
	middlewarestd "github.com/slok/go-http-metrics/middleware/std"
)

var logger = logging.Logger("someguy")

func withRequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := httpsnoop.CaptureMetrics(next, w, r)
		logger.Debugw(r.Method, "url", r.URL, "host", r.Host, "code", m.Code, "duration", m.Duration, "written", m.Written, "ua", r.UserAgent(), "referer", r.Referer())
	})
}

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

	mdlw := middleware.New(middleware.Config{
		Recorder: metrics.NewRecorder(metrics.Config{Prefix: "someguy"}),
	})

	handler := server.Handler(&composableRouter{
		providers: crRouters,
		peers:     prRouters,
		ipns:      ipnsRouters,
	})

	// Add CORS.
	handler = cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{http.MethodGet, http.MethodOptions},
		MaxAge:         86400,
	}).Handler(handler)

	// Add compression.
	compress, err := httpcompression.DefaultAdapter()
	if err != nil {
		return err
	}
	handler = compress(handler)

	// Add metrics.
	handler = middlewarestd.Handler("/", mdlw, handler)

	// Add request logging.
	handler = withRequestLogger(handler)

	http.Handle("/debug/metrics/prometheus", promhttp.Handler())
	http.Handle("/", handler)
	server := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: nil}
	return server.ListenAndServe()
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
