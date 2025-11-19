// autoconf.go implements automatic configuration for someguy.
//
// Autoconf fetches network configuration from a remote JSON endpoint to automatically
// configure bootstrap peers and delegated routing endpoints.
//
// The autoconf system:
//   - Fetches configuration from a remote URL (configurable)
//   - Caches configuration locally and refreshes periodically
//   - Falls back to embedded defaults if fetching fails
//   - Expands "auto" placeholder in endpoint configuration
//   - Filters out endpoints for systems running natively (e.g., DHT)
//   - Validates and normalizes endpoint URLs
//
// See https://github.com/ipfs/someguy/blob/main/docs/environment-variables.md
// for configuration options and defaults.
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ipfs/boxo/autoconf"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// autoConfConfig contains the configuration for the autoconf subsystem.
type autoConfConfig struct {
	// enabled determines whether to use autoconf
	// Default: true
	enabled bool

	// url is the HTTP(S) URL to fetch the autoconf.json from
	// Default: https://conf.ipfs-mainnet.org/autoconf.json
	url string

	// refreshInterval is how often to refresh autoconf data
	// Default: 24h
	refreshInterval time.Duration

	// cacheDir is the directory to cache autoconf data
	// Default: $SOMEGUY_DATADIR/.autoconf-cache
	cacheDir string
}

func startAutoConf(ctx context.Context, cfg *config) (*autoconf.Config, error) {
	var autoConf *autoconf.Config
	if cfg.autoConf.enabled && cfg.autoConf.url != "" {
		client, err := createAutoConfClient(cfg.autoConf)
		if err != nil {
			return nil, fmt.Errorf("failed to create autoconf client: %w", err)
		}
		// Start primes cache and starts background updater
		// Note: Start() always returns a config (using fallback if needed)
		autoConf, err = client.Start(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to start autoconf updater: %w", err)
		}
	}
	return autoConf, nil
}

func getBootstrapPeerAddrInfos(cfg *config, autoConf *autoconf.Config) []peer.AddrInfo {
	if autoConf != nil {
		nativeSystems := getNativeSystems(cfg.dhtType)
		return stringsToPeerAddrInfos(autoConf.GetBootstrapPeers(nativeSystems...))
	}
	// Fallback to autoconf fallback bootstrappers.
	return stringsToPeerAddrInfos(autoconf.FallbackBootstrapPeers)
}

// normalizeEndpointURL validates and normalizes a single endpoint URL for a specific routing type.
// Returns the base URL (with routing path stripped if present) and an error if the URL has a mismatched path.
func normalizeEndpointURL(url, expectedPath, flagName string) (string, error) {
	// "auto" placeholder passes through unchanged
	if url == autoconf.AutoPlaceholder {
		return url, nil
	}

	// Check if URL has the expected routing path
	if strings.HasSuffix(url, expectedPath) {
		// Strip the expected path to get base URL
		return strings.TrimSuffix(url, expectedPath), nil
	}

	// Check if URL has a different routing path (potential misconfiguration)
	routingPaths := []string{
		autoconf.RoutingV1ProvidersPath,
		autoconf.RoutingV1PeersPath,
		autoconf.RoutingV1IPNSPath,
	}
	for _, path := range routingPaths {
		if strings.HasSuffix(url, path) {
			return "", fmt.Errorf("URL %q has path %q which doesn't match %s (expected %q or no path)", url, path, flagName, expectedPath)
		}
	}

	// URL has no routing path or unknown path - treat as base URL
	return url, nil
}

// validateEndpointURLs validates and normalizes a list of endpoint URLs for a specific routing type
func validateEndpointURLs(urls []string, expectedPath, flagName, envVar string) ([]string, error) {
	normalized := make([]string, 0, len(urls))
	for _, url := range urls {
		baseURL, err := normalizeEndpointURL(url, expectedPath, flagName)
		if err != nil {
			return nil, fmt.Errorf("%s: %w. Use %s or %s to fix", envVar, err, envVar, flagName)
		}
		normalized = append(normalized, baseURL)
	}
	return normalized, nil
}

// expandDelegatedRoutingEndpoints expands autoconf placeholders and categorizes endpoints by path
func expandDelegatedRoutingEndpoints(cfg *config, autoConf *autoconf.Config) error {
	// Validate and normalize each flag's URLs separately
	normalizedProviders, err := validateEndpointURLs(cfg.contentEndpoints, autoconf.RoutingV1ProvidersPath, "--provider-endpoints", "SOMEGUY_PROVIDER_ENDPOINTS")
	if err != nil {
		return err
	}

	normalizedPeers, err := validateEndpointURLs(cfg.peerEndpoints, autoconf.RoutingV1PeersPath, "--peer-endpoints", "SOMEGUY_PEER_ENDPOINTS")
	if err != nil {
		return err
	}

	normalizedIPNS, err := validateEndpointURLs(cfg.ipnsEndpoints, autoconf.RoutingV1IPNSPath, "--ipns-endpoints", "SOMEGUY_IPNS_ENDPOINTS")
	if err != nil {
		return err
	}

	if !cfg.autoConf.enabled {
		// Check for "auto" placeholder when autoconf is disabled
		if slices.Contains(normalizedProviders, autoconf.AutoPlaceholder) ||
			slices.Contains(normalizedPeers, autoconf.AutoPlaceholder) ||
			slices.Contains(normalizedIPNS, autoconf.AutoPlaceholder) {
			return autoconfDisabledError("endpoint option", "SOMEGUY_PROVIDER_ENDPOINTS/SOMEGUY_PEER_ENDPOINTS/SOMEGUY_IPNS_ENDPOINTS", "--provider-endpoints/--peer-endpoints/--ipns-endpoints")
		}
		// No autoconf, keep normalized endpoints as configured
		cfg.contentEndpoints = deduplicateEndpoints(normalizedProviders)
		cfg.peerEndpoints = deduplicateEndpoints(normalizedPeers)
		cfg.ipnsEndpoints = deduplicateEndpoints(normalizedIPNS)
		return nil
	}

	nativeSystems := getNativeSystems(cfg.dhtType)

	// Expand each routing type separately to maintain category information
	expandedProviders := autoconf.ExpandDelegatedEndpoints(
		normalizedProviders,
		autoConf,
		nativeSystems,
		autoconf.RoutingV1ProvidersPath,
	)

	expandedPeers := autoconf.ExpandDelegatedEndpoints(
		normalizedPeers,
		autoConf,
		nativeSystems,
		autoconf.RoutingV1PeersPath,
	)

	expandedIPNS := autoconf.ExpandDelegatedEndpoints(
		normalizedIPNS,
		autoConf,
		nativeSystems,
		autoconf.RoutingV1IPNSPath,
	)

	// Strip routing paths from expanded URLs to get base URLs
	cfg.contentEndpoints = stripRoutingPaths(expandedProviders, autoconf.RoutingV1ProvidersPath)
	cfg.peerEndpoints = stripRoutingPaths(expandedPeers, autoconf.RoutingV1PeersPath)
	cfg.ipnsEndpoints = stripRoutingPaths(expandedIPNS, autoconf.RoutingV1IPNSPath)

	logger.Debugf("expanded endpoints - providers: %v, peers: %v, IPNS: %v",
		cfg.contentEndpoints, cfg.peerEndpoints, cfg.ipnsEndpoints)

	return nil
}

// stripRoutingPaths strips the routing path from URLs and deduplicates
// URLs without the expected path are kept as base URLs (from custom config)
// Handles trailing slashes by normalizing before comparison
func stripRoutingPaths(urls []string, expectedPath string) []string {
	result := make([]string, 0, len(urls))
	for _, url := range urls {
		// Trim trailing slash for comparison
		normalized := strings.TrimSuffix(url, "/")
		if strings.HasSuffix(normalized, expectedPath) {
			// Autoconf-expanded URL with path - strip it
			result = append(result, strings.TrimSuffix(normalized, expectedPath))
		} else {
			// Custom base URL without path - keep normalized (no trailing slash)
			result = append(result, normalized)
		}
	}
	return deduplicateEndpoints(result)
}

// deduplicateEndpoints removes duplicate endpoints from a list
func deduplicateEndpoints(endpoints []string) []string {
	if len(endpoints) == 0 {
		return endpoints
	}
	slices.Sort(endpoints)
	return slices.Compact(endpoints)
}

// autoconfDisabledError returns a consistent error message when auto placeholder is found but autoconf is disabled
func autoconfDisabledError(configType, envVar, flag string) error {
	return fmt.Errorf("'auto' placeholder found in %s but autoconf is disabled. Set explicit %s with %s or %s, or re-enable autoconf",
		configType, configType, envVar, flag)
}

func stringsToPeerAddrInfos(addrs []string) []peer.AddrInfo {
	addrInfos := make([]peer.AddrInfo, 0, len(addrs))

	for _, s := range addrs {
		ma, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			logger.Error("bad multiaddr in bootstrapper autoconf data", "err", err)
			continue
		}

		info, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			logger.Errorw("failed to convert bootstrapper address to peer addr info", "address", ma.String(), err, "err")
			continue
		}
		addrInfos = append(addrInfos, *info)
	}

	return addrInfos
}

// createAutoConfClient creates an autoconf client with the given configuration
func createAutoConfClient(cfg autoConfConfig) (*autoconf.Client, error) {
	if cfg.cacheDir == "" {
		cfg.cacheDir = filepath.Join(".", ".autoconf-cache")
	}
	if cfg.refreshInterval == 0 {
		cfg.refreshInterval = autoconf.DefaultRefreshInterval
	}
	if cfg.url == "" {
		cfg.url = autoconf.MainnetAutoConfURL
	}

	return autoconf.NewClient(
		autoconf.WithCacheDir(cfg.cacheDir),
		autoconf.WithUserAgent("someguy/"+version),
		autoconf.WithCacheSize(autoconf.DefaultCacheSize),
		autoconf.WithTimeout(autoconf.DefaultTimeout),
		autoconf.WithURL(cfg.url),
		autoconf.WithRefreshInterval(cfg.refreshInterval),
		autoconf.WithFallback(autoconf.GetMainnetFallbackConfig),
	)
}

// getNativeSystems returns the list of systems that should be used natively based on routing type
func getNativeSystems(routingType string) []string {
	switch routingType {
	case "dht", "accelerated", "standard", "auto":
		return []string{autoconf.SystemAminoDHT}
	case "disabled", "off", "none", "delegated", "custom":
		return []string{}
	default:
		logger.Warnf("getNativeSystems: unknown routing type %q, assuming no native systems", routingType)
		return []string{}
	}
}
