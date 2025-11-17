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

// autoConfConfig contains the configuration for the autoconf subsystem
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

func expandContentEndpoints(cfg *config, autoConf *autoconf.Config) error {
	if !cfg.autoConf.enabled {
		if slices.Contains(cfg.contentEndpoints, autoconf.AutoPlaceholder) {
			return autoconfDisabledError("endpoint option", "SOMEGUY_DELEGATED_ENDPOINT", "--endpoint")
		}
		return nil
	}

	nativeSystems := getNativeSystems(cfg.dhtType)

	// Someguy only uses read-only endpoints for providers, peers, and IPNS
	cfg.contentEndpoints = autoconf.ExpandDelegatedEndpoints(cfg.contentEndpoints, autoConf, nativeSystems,
		autoconf.RoutingV1ProvidersPath,
		autoconf.RoutingV1PeersPath,
		autoconf.RoutingV1IPNSPath)

	// Need to remove "/routing/v1/providers" because routing.FindProviders adds it back on.
	for i, ep := range cfg.contentEndpoints {
		ep = strings.TrimSuffix(ep, autoconf.RoutingV1ProvidersPath)
		ep = strings.TrimSuffix(ep, autoconf.RoutingV1PeersPath)
		ep = strings.TrimSuffix(ep, autoconf.RoutingV1IPNSPath)
		cfg.contentEndpoints[i] = ep
	}

	// Remove duplicates.
	slices.Sort(cfg.contentEndpoints)
	cfg.contentEndpoints = slices.Compact(cfg.contentEndpoints)

	return nil
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
func createAutoConfClient(config autoConfConfig) (*autoconf.Client, error) {
	if config.cacheDir == "" {
		config.cacheDir = filepath.Join(".", ".autoconf-cache")
	}
	if config.refreshInterval == 0 {
		config.refreshInterval = autoconf.DefaultRefreshInterval
	}
	if config.url == "" {
		config.url = autoconf.MainnetAutoConfURL
	}

	return autoconf.NewClient(
		autoconf.WithCacheDir(config.cacheDir),
		autoconf.WithUserAgent("someguy/"+version),
		autoconf.WithCacheSize(autoconf.DefaultCacheSize),
		autoconf.WithTimeout(autoconf.DefaultTimeout),
		autoconf.WithURL(config.url),
		autoconf.WithRefreshInterval(config.refreshInterval),
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
