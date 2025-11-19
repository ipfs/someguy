package main

import (
	"context"
	"fmt"

	drclient "github.com/ipfs/boxo/routing/http/client"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.opencensus.io/stats/view"
)

// clientRouter wraps an HTTP delegated routing client to implement the router interface.
// Only FindProviders and FindPeers are explicitly implemented to adapt the signature
// (our interface includes a limit parameter). The IPNS methods (GetIPNS/PutIPNS) are
// inherited from the embedded drclient.Client as their signatures already match.
var _ router = clientRouter{}

type clientRouter struct {
	*drclient.Client
}

func (d clientRouter) FindProviders(ctx context.Context, cid cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	return d.Client.FindProviders(ctx, cid)
}

func (d clientRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	return d.Client.FindPeers(ctx, pid)
}

// endpointConfig tracks which routing capabilities a base URL should provide
type endpointConfig struct {
	baseURL   string
	providers bool // FindProviders capability
	peers     bool // FindPeer capability
	ipns      bool // GetIPNS/PutIPNS capability
}

// collectEndpoints deduplicates base URLs across all endpoint types and
// aggregates their capabilities. This ensures we create only one HTTP client
// per unique base URL, even if it appears in multiple endpoint configurations.
func collectEndpoints(cfg *config) []endpointConfig {
	capabilities := make(map[string]*endpointConfig)

	// Collect provider endpoints
	for _, url := range cfg.contentEndpoints {
		if url == "" {
			continue // skip empty strings
		}
		if caps := capabilities[url]; caps != nil {
			caps.providers = true
		} else {
			capabilities[url] = &endpointConfig{baseURL: url, providers: true}
		}
	}

	// Collect peer endpoints
	for _, url := range cfg.peerEndpoints {
		if url == "" {
			continue // skip empty strings
		}
		if caps := capabilities[url]; caps != nil {
			caps.peers = true
		} else {
			capabilities[url] = &endpointConfig{baseURL: url, peers: true}
		}
	}

	// Collect IPNS endpoints
	for _, url := range cfg.ipnsEndpoints {
		if url == "" {
			continue // skip empty strings
		}
		if caps := capabilities[url]; caps != nil {
			caps.ipns = true
		} else {
			capabilities[url] = &endpointConfig{baseURL: url, ipns: true}
		}
	}

	// Convert map to slice
	result := make([]endpointConfig, 0, len(capabilities))
	for _, caps := range capabilities {
		result = append(result, *caps)
	}

	return result
}

// createDelegatedHTTPRouters creates deduplicated HTTP routing clients.
// It ensures that each unique base URL gets exactly one HTTP client, even if
// that URL appears in multiple endpoint configurations (provider/peer/ipns).
// The same client instance is added to multiple router lists based on its
// aggregated capabilities.
func createDelegatedHTTPRouters(cfg *config) (providers, peers, ipns []router, err error) {
	endpoints := collectEndpoints(cfg)

	var providerRouters, peerRouters, ipnsRouters []router

	for _, endpoint := range endpoints {
		// Create ONE HTTP client per unique base URL
		client, err := drclient.New(
			endpoint.baseURL,
			drclient.WithUserAgent("someguy/"+buildVersion()),
			// override default filters, we want all results from remote endpoint
			drclient.WithProtocolFilter([]string{}),
			drclient.WithDisabledLocalFiltering(true),
		)
		if err != nil {
			return nil, nil, nil, err
		}

		// Wrap in clientRouter - this implements all routing interfaces
		router := clientRouter{Client: client}

		// Add the same router instance to appropriate lists based on capabilities
		if endpoint.providers {
			providerRouters = append(providerRouters, router)
		}
		if endpoint.peers {
			peerRouters = append(peerRouters, router)
		}
		if endpoint.ipns {
			ipnsRouters = append(ipnsRouters, router)
		}
	}

	// Register delegated routing client metrics only once for all HTTP clients.
	// We must avoid registering multiple times since view.Register() is a global operation.
	if len(providerRouters) > 0 || len(peerRouters) > 0 || len(ipnsRouters) > 0 {
		if err := view.Register(drclient.OpenCensusViews...); err != nil {
			return nil, nil, nil, fmt.Errorf("registering HTTP delegated routing views: %w", err)
		}
	}

	return providerRouters, peerRouters, ipnsRouters, nil
}
