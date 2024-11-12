package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/CAFxX/httpcompression"
	sddaemon "github.com/coreos/go-systemd/v22/daemon"
	"github.com/felixge/httpsnoop"
	drclient "github.com/ipfs/boxo/routing/http/client"
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
		logger.Debugw(r.Method, "url", r.URL, "host", r.Host, "code", m.Code, "duration", m.Duration, "written", m.Written, "accept", r.Header.Get("Accept"), "ua", r.UserAgent(), "referer", r.Referer())
	})
}

type config struct {
	listenAddress        string
	acceleratedDHTClient bool

	contentEndpoints []string
	peerEndpoints    []string
	ipnsEndpoints    []string

	libp2pListenAddress []string
	connMgrLow          int
	connMgrHi           int
	connMgrGrace        time.Duration
	maxMemory           uint64
	maxFD               int
}

func start(ctx context.Context, cfg *config) error {
	h, err := newHost(cfg)
	if err != nil {
		return err
	}

	fmt.Printf("Someguy libp2p host listening on %v\n", h.Addrs())
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

	mdlw := middleware.New(middleware.Config{
		Recorder: metrics.NewRecorder(metrics.Config{
			Prefix:          "someguy",
			DurationBuckets: []float64{0.05, 0.1, 0.5, 1, 5, 10, 20, 30, 40, 50, 60},
		}),
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
	quit := make(chan os.Signal, 3)
	var wg sync.WaitGroup
	wg.Add(1)

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

func getCombinedRouting(endpoints []string, dht routing.Routing) (router, error) {
	if len(endpoints) == 0 {
		return sanitizeRouter{libp2pRouter{routing: dht}}, nil
	}

	var routers []router

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
		routers = append(routers, clientRouter{Client: drclient})
	}

	return sanitizeRouter{parallelRouter{
		routers: append(routers, libp2pRouter{routing: dht}),
	}}, nil
}
