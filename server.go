package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/CAFxX/httpcompression"
	"github.com/felixge/httpsnoop"
	"github.com/ipfs/boxo/routing/http/client"
	"github.com/ipfs/boxo/routing/http/server"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	metrics "github.com/slok/go-http-metrics/metrics/prometheus"
	"github.com/slok/go-http-metrics/middleware"
	middlewarestd "github.com/slok/go-http-metrics/middleware/std"
)

var logger = logging.Logger(name)

func withRequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := httpsnoop.CaptureMetrics(next, w, r)
		logger.Debugw(r.Method, "url", r.URL, "host", r.Host, "code", m.Code, "duration", m.Duration, "written", m.Written, "ua", r.UserAgent(), "referer", r.Referer())
	})
}

type config struct {
	listenAddress        string
	acceleratedDHTClient bool

	contentEndpoints []string
	peerEndpoints    []string
	ipnsEndpoints    []string

	connMgrLow   int
	connMgrHi    int
	connMgrGrace time.Duration
	maxMemory    uint64
	maxFD        int
}

func start(ctx context.Context, cfg *config) error {
	h, err := newHost(cfg)
	if err != nil {
		return err
	}

	var dhtRouting routing.Routing
	if cfg.acceleratedDHTClient {
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

	crRouters, err := getCombinedRouting(cfg.contentEndpoints, dhtRouting)
	if err != nil {
		return err
	}

	prRouters, err := getCombinedRouting(cfg.peerEndpoints, dhtRouting)
	if err != nil {
		return err
	}

	ipnsRouters, err := getCombinedRouting(cfg.ipnsEndpoints, dhtRouting)
	if err != nil {
		return err
	}

	_, port, err := net.SplitHostPort(cfg.listenAddress)
	if err != nil {
		return err
	}

	log.Printf("Starting %s %s\n", name, version)
	log.Printf("Listening on %s", cfg.listenAddress)
	log.Printf("Delegated Routing API on http://127.0.0.1:%s/routing/v1", port)

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
		MaxAge:         600,
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
	http.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Client: %s\n", name)
		fmt.Fprintf(w, "Version: %s\n", version)
	})
	http.Handle("/", handler)

	server := &http.Server{Addr: cfg.listenAddress, Handler: nil}
	return server.ListenAndServe()
}

func newHost(cfg *config) (host.Host, error) {
	cmgr, err := connmgr.NewConnManager(cfg.connMgrLow, cfg.connMgrHi, connmgr.WithGracePeriod(cfg.connMgrGrace))
	if err != nil {
		return nil, err
	}

	rcmgr, err := makeResourceMgrs(cfg.maxMemory, cfg.maxFD, cfg.connMgrHi)
	if err != nil {
		return nil, err
	}

	h, err := libp2p.New(
		libp2p.ConnectionManager(cmgr),
		libp2p.ResourceManager(rcmgr),
	)
	if err != nil {
		return nil, err
	}

	return h, nil
}

func getCombinedRouting(endpoints []string, dht routing.Routing) (router, error) {
	if len(endpoints) == 0 {
		return sanitizeRouter{libp2pRouter{routing: dht}}, nil
	}

	var routers []router

	for _, endpoint := range endpoints {
		drclient, err := client.New(endpoint, client.WithUserAgent(userAgent))
		if err != nil {
			return nil, err
		}
		routers = append(routers, clientRouter{Client: drclient})
	}

	return sanitizeRouter{parallelRouter{
		routers: append(routers, libp2pRouter{routing: dht}),
	}}, nil
}
