package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	ocprom "contrib.go.opencensus.io/exporter/prometheus"
	"github.com/CAFxX/httpcompression"
	sddaemon "github.com/coreos/go-systemd/v22/daemon"
	"github.com/felixge/httpsnoop"
	drclient "github.com/ipfs/boxo/routing/http/client"
	"github.com/ipfs/boxo/routing/http/server"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/libp2p/go-libp2p/gologshim"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	"go.opencensus.io/stats/view"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var logger = logging.Logger(name)

func init() {
	// Set go-log's slog handler as the application-wide default.
	// This ensures all slog-based logging uses go-log's formatting.
	slog.SetDefault(slog.New(logging.SlogHandler()))

	// Wire go-log's slog bridge to go-libp2p's gologshim.
	// This provides go-libp2p loggers with the "logger" attribute
	// for per-subsystem level control.
	gologshim.SetDefaultHandler(logging.SlogHandler())

	// setup opencensus -> prometheus forwarding for delegated routing metrics
	promRegistry, ok := prometheus.DefaultRegisterer.(*prometheus.Registry)
	if !ok {
		logger.Error("delegated routing metrics: error casting DefaultRegisterer")
		return
	}
	pe, err := ocprom.NewExporter(ocprom.Options{
		Namespace: "someguy",
		Registry:  promRegistry,
		OnError: func(err error) {
			logger.Errorf("ocprom error: %w", err)
		},
	})
	if err != nil {
		logger.Errorf("delegated routing metrics: error creating exporter: %w", err)
		return
	}
	view.RegisterExporter(pe)
	view.SetReportingPeriod(2 * time.Second)
}

func withRequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := httpsnoop.CaptureMetrics(next, w, r)
		logger.Debugw(r.Method, "url", r.URL, "host", r.Host, "code", m.Code, "duration", m.Duration, "written", m.Written, "accept", r.Header.Get("Accept"), "ua", r.UserAgent(), "referer", r.Referer())
	})
}

type config struct {
	listenAddress               string
	dhtType                     string
	cachedAddrBook              bool
	cachedAddrBookActiveProbing bool
	cachedAddrBookRecentTTL     time.Duration

	contentEndpoints       []string
	peerEndpoints          []string
	ipnsEndpoints          []string
	blockProviderEndpoints []string
	blockProviderPeerIDs   []string

	libp2pListenAddress []string
	connMgrLow          int
	connMgrHi           int
	connMgrGrace        time.Duration
	maxMemory           uint64
	maxFD               int

	tracingAuth      string
	samplingFraction float64

	autoConf autoConfConfig
}

func start(ctx context.Context, cfg *config) error {
	h, err := newHost(cfg)
	if err != nil {
		return err
	}

	autoConf, err := startAutoConf(ctx, cfg)
	if err != nil {
		logger.Error(err.Error())
	}

	bootstrapAddrInfos := getBootstrapPeerAddrInfos(cfg, autoConf)
	if err = expandContentEndpoints(cfg, autoConf); err != nil {
		return err
	}

	fmt.Printf("Someguy libp2p host listening on %v\n", h.Addrs())
	var dhtRouting routing.Routing
	switch cfg.dhtType {
	case "accelerated":
		wrappedDHT, err := newBundledDHT(ctx, h, bootstrapAddrInfos)
		if err != nil {
			return err
		}
		dhtRouting = wrappedDHT
	case "standard":
		standardDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeClient), dht.BootstrapPeers(bootstrapAddrInfos...))
		if err != nil {
			return err
		}
		dhtRouting = standardDHT
	case "disabled":
	default:
		return fmt.Errorf("invalid dht type %s, must be one of [accelerated, standard, disabled]", cfg.dhtType)
	}

	var cachedAddrBook *cachedAddrBook

	if cfg.cachedAddrBook && dhtRouting != nil {
		fmt.Printf("Using cached address book to speed up provider discovery (active probing enabled: %t)\n", cfg.cachedAddrBookActiveProbing)
		opts := []AddrBookOption{}

		if cfg.cachedAddrBookRecentTTL > 0 {
			opts = append(opts, WithRecentlyConnectedTTL(cfg.cachedAddrBookRecentTTL))
		}

		opts = append(opts, WithActiveProbing(cfg.cachedAddrBookActiveProbing))

		cachedAddrBook, err = newCachedAddrBook(opts...)
		if err != nil {
			return err
		}
		go cachedAddrBook.background(ctx, h)
	}

	var blockProviderRouters []router
	if len(cfg.blockProviderEndpoints) > 0 {
		if len(cfg.blockProviderPeerIDs) != len(cfg.blockProviderEndpoints) {
			return fmt.Errorf("number of block provider peer IDs must match number of endpoints")
		}
		for i, endpoint := range cfg.blockProviderEndpoints {
			p, err := peer.Decode(cfg.blockProviderPeerIDs[i])
			if err != nil {
				return fmt.Errorf("invalid peer ID %s: %w", cfg.blockProviderPeerIDs[i], err)
			}

			r, err := newHTTPBlockRouter(endpoint, p, nil)
			if err != nil {
				return err
			}
			blockProviderRouters = append(blockProviderRouters, composableRouter{providers: r})
		}
	}

	crRouters, err := getCombinedRouting(cfg.contentEndpoints, dhtRouting, cachedAddrBook, blockProviderRouters)
	if err != nil {
		return err
	}

	prRouters, err := getCombinedRouting(cfg.peerEndpoints, dhtRouting, cachedAddrBook, nil)
	if err != nil {
		return err
	}

	ipnsRouters, err := getCombinedRouting(cfg.ipnsEndpoints, dhtRouting, cachedAddrBook, nil)
	if err != nil {
		return err
	}

	_, port, err := net.SplitHostPort(cfg.listenAddress)
	if err != nil {
		return err
	}

	tp, err := setupTracing(ctx, cfg.samplingFraction)
	if err != nil {
		return err
	}

	defer func() {
		_ = tp.Shutdown(ctx)
	}()

	handlerOpts := []server.Option{
		server.WithPrometheusRegistry(prometheus.DefaultRegisterer),
	}

	handler := server.Handler(&composableRouter{
		providers: crRouters,
		peers:     prRouters,
		ipns:      ipnsRouters,
	}, handlerOpts...)

	// Add CORS.
	handler = cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{http.MethodGet, http.MethodOptions, http.MethodPut},
		MaxAge:         600,
	}).Handler(handler)

	// Add compression.
	compress, err := httpcompression.DefaultAdapter()
	if err != nil {
		return err
	}
	handler = compress(handler)

	// Add request logging.
	handler = withRequestLogger(handler)

	// Add request tracing
	handler = withTracingAndDebug(handler, cfg.tracingAuth)

	http.Handle("/", handler)

	http.Handle("/debug/metrics/prometheus", promhttp.Handler())
	http.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Client: %s\n", name)
		fmt.Fprintf(w, "Version: %s\n", version)
	})

	server := &http.Server{Addr: cfg.listenAddress, Handler: nil}
	quit := make(chan os.Signal, 3)
	var wg sync.WaitGroup
	wg.Add(1)

	fmt.Printf("Metrics endpoint: http://127.0.0.1:%s/debug/metrics/prometheus\n", port)
	fmt.Printf("Delegated Routing API on http://127.0.0.1:%s/routing/v1\n", port)

	go func() {
		defer wg.Done()
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("Failed to start /routing/v1 server: %v", err)
			quit <- os.Interrupt
		}
	}()

	sddaemon.SdNotify(false, sddaemon.SdNotifyReady)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	<-quit
	sddaemon.SdNotify(false, sddaemon.SdNotifyStopping)
	fmt.Printf("\nClosing /routing/v1 server...\n")

	// Attempt a graceful shutdown
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Graceful shutdown failed:%+v\n", err)
	}

	go server.Close()
	wg.Wait()
	fmt.Println("Shutdown finished.")
	return nil
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

	opts := []libp2p.Option{
		libp2p.UserAgent("someguy/" + buildVersion()),
		libp2p.ConnectionManager(cmgr),
		libp2p.ResourceManager(rcmgr),
		libp2p.NATPortMap(),
		libp2p.DefaultTransports,
		libp2p.DefaultMuxers,
		libp2p.EnableHolePunching(),
	}

	if len(cfg.libp2pListenAddress) == 0 {
		// Note: because the transports are set above we must also set the listen addresses
		// We need to set listen addresses in order for hole punching to work
		opts = append(opts, libp2p.DefaultListenAddrs)
	} else {
		opts = append(opts, libp2p.ListenAddrStrings(cfg.libp2pListenAddress...))
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, err
	}

	return h, nil
}

func getCombinedRouting(endpoints []string, dht routing.Routing, cachedAddrBook *cachedAddrBook, additionalRouters []router) (router, error) {
	var dhtRouter router

	if cachedAddrBook != nil {
		cachedRouter := NewCachedRouter(libp2pRouter{routing: dht}, cachedAddrBook)
		dhtRouter = sanitizeRouter{cachedRouter}
	} else if dht != nil {
		dhtRouter = sanitizeRouter{libp2pRouter{routing: dht}}
	}

	if len(endpoints) == 0 && len(additionalRouters) == 0 {
		if dhtRouter == nil {
			return composableRouter{}, nil
		}
		return dhtRouter, nil
	}

	var delegatedRouters []router

	for _, endpoint := range endpoints {
		drclient, err := drclient.New(endpoint,
			drclient.WithUserAgent("someguy/"+buildVersion()),
			// override default filters, we want all results from remote endpoint, then someguy's user can use IPIP-484 to narrow them down
			drclient.WithProtocolFilter([]string{}),
			drclient.WithDisabledLocalFiltering(true),
		)
		if err != nil {
			return nil, err
		}
		delegatedRouters = append(delegatedRouters, clientRouter{Client: drclient})
	}

	// setup delegated routing client metrics
	if err := view.Register(drclient.OpenCensusViews...); err != nil {
		return nil, fmt.Errorf("registering HTTP delegated routing views: %w", err)
	}

	var routers []router
	routers = append(routers, delegatedRouters...)
	if dhtRouter != nil {
		routers = append(routers, dhtRouter)
	}
	routers = append(routers, additionalRouters...)

	return parallelRouter{
		routers: routers,
	}, nil
}

func withTracingAndDebug(next http.Handler, authToken string) http.Handler {
	next = otelhttp.NewHandler(next, "someguy.request")

	// Remove tracing and cache skipping headers if not authorized
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Disable tracing/debug headers if auth token missing or invalid
		if authToken == "" || request.Header.Get("Authorization") != authToken {
			if request.Header.Get("Traceparent") != "" {
				request.Header.Del("Traceparent")
			}
			if request.Header.Get("Tracestate") != "" {
				request.Header.Del("Tracestate")
			}
		}

		next.ServeHTTP(writer, request)
	})
}
