package main

import (
	"path/filepath"
	"time"

	autoconf "github.com/ipfs/boxo/autoconf"
	dht "github.com/libp2p/go-libp2p-kad-dht"
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
	// Default: $RAINBOW_DATADIR/.autoconf-cache
	cacheDir string
}

func getBootstrapPeerAddrInfos(cfg *config, autoConf *autoconf.Config) []peer.AddrInfo {
	if autoConf != nil {
		nativeSystems := getNativeSystems(cfg.dhtType)
		return stringsToPeerAddrInfos(autoConf.GetBootstrapPeers(nativeSystems...))
	}
	// Fallback to hard-coded bootstrappers.
	return dht.GetDefaultBootstrapPeerAddrInfos()
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
	)
}

// getNativeSystems returns the list of systems that should be used natively based on routing type
func getNativeSystems(routingType string) []string {
	switch routingType {
	case "dht", "accelerated", "standard", "auto":
		return []string{autoconf.SystemAminoDHT}
	case "disabled", "off", "none", "custom":
		return []string{}
	default:
		logger.Warnf("getNativeSystems: unknown routing type %q, assuming no native systems", routingType)
		return []string{}
	}
}
