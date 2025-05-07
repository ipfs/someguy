# Changelog

All notable changes to this project will be documented in this file.

Note:
* The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
* This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Legend
The following emojis are used to highlight certain changes:
* 🛠 - BREAKING CHANGE.  Action is required if you use this functionality.
* ✨ - Noteworthy change to be aware of.

## [Unreleased]

### Added

- Added `http-block-provider-endpoints` and `http-block-provider-peerids` options to allow using a trustless gateway block provider as a content routing record source

### Changed

- `accelerated-dht` option was removed and replaced with a `dht` option which enables toggling between the standard client, accelerated client and being disabled

### Removed

### Fixed

### Security

## [v0.8.1]

This release includes a number of dependency updates that include bug fixes and improvements.

### Changed

- [go-libp2p 0.41.0](https://github.com/libp2p/go-libp2p/releases/tag/v0.41.0)
- [go-libp2p-kad-dht v0.30.0](https://github.com/libp2p/go-libp2p-kad-dht/releases/tag/v0.30.0)
- [Boxo v0.28.0](https://github.com/ipfs/boxo/releases/tag/v0.28.0)

## [v0.8.0]

### Added

- Enabled CORS for PUT requests to `/routing/v1/ipns`.

## [v0.7.1]

### Fixed

- Fix a bug whereby, cached peers with private multiaddrs were returned in `/routing/v1/providers` responses, as they were not passing through `sanitizeRouter`.

## [v0.7.0]

### Added

- Peer addresses are cached for 48h to match [provider record expiration on Amino DHT](https://github.com/libp2p/go-libp2p-kad-dht/blob/v0.28.1/amino/defaults.go#L40-L43).
- In the background, someguy probes cached peers at most once per hour (`PeerProbeThreshold`) by attempting to dial them to keep their multiaddrs up to date. If a peer is not reachable, an exponential backoff is applied to reduce the frequency of probing. If a cached peer is unreachable for more than 48h (`MaxBackoffDuration`), it is removed from the cache.
- Someguy now augments providers missing addresses in `FindProviders` with cached addresses. If a peer is encountered with no cached addresses, `FindPeer` is dispatched in the background and the result is streamed in the reponse. Providers for which no addresses can be found, are omitted from the response.
  - This can be enabled via `SOMEGUY_CACHED_ADDR_BOOK=true|false` (enabled by default)
  - Two additional configuration options for the  `cachedAddrBook` implementation:
    - `SOMEGUY_CACHED_ADDR_BOOK_ACTIVE_PROBING` whether to actively probe cached peers in the background to keep their multiaddrs up to date.
    - `SOMEGUY_CACHED_ADDR_BOOK_RECENT_TTL` to adjust the TTL for cached addresses of recently connected peers.

## [v0.6.0]

### Added

- Add request tracing with sampling or require token for requests with Traceparent header. See [tracing.md](./docs/tracing.md) for more details.

### Changed

- go-libp2p-kad-dht updated to [v0.28.1](https://github.com/libp2p/go-libp2p-kad-dht/releases/tag/v0.28.1)
- Metrics `someguy_http_request_duration_seconds` and `someguy_http_response_size_bytes` were replaced with  `delegated_routing_server_http_request_duration_seconds` and `delegated_routing_server_http_response_size_bytes` from upstream `boxo/routing/http/server`.


## [v0.5.3]

### Fixed

- default config: restore proxying of all results from IPNI at `cid.contact` [#83](https://github.com/ipfs/someguy/pull/85)

## [v0.5.2]

### Changed

- [go-libp2p 0.37.0](https://github.com/libp2p/go-libp2p/releases/tag/v0.37.0)
- [boxo v0.24.1](https://github.com/ipfs/boxo/releases/tag/v0.24.1)

## [v0.5.0]

### Added

- Added support for [IPIP-484](https://github.com/ipfs/specs/pull/484) which allows filtering network transports (addresses) and transfer protocols (bitswap, etc) in `/routing/v1/` responses. [#82](https://github.com/ipfs/someguy/pull/82/)

### Changed

- [go-libp2p 0.36.5](https://github.com/libp2p/go-libp2p/releases/tag/v0.36.5)
- [Boxo v0.24.0](https://github.com/ipfs/boxo/releases/tag/v0.24.0)

## [v0.4.2]

### Fixed

- [go-libp2p-kad-dht v0.26.1](https://github.com/libp2p/go-libp2p-kad-dht/releases/tag/v0.26.1) fixes a bug where `FindPeer` did not return results for peers behind NAT which only have p2p-circuit multiaddrs. [#80](https://github.com/ipfs/someguy/pull/80)

## [v0.4.1]

### Added

- `SOMEGUY_LIBP2P_LISTEN_ADDRS` config [environment variable](./docs/environment-variables.md#someguy_libp2p_listen_addrs) for customizing the interfaces, ports, and transports of the libp2p host created by someguy. [#79](https://github.com/ipfs/someguy/pull/79)

### Fixed

- enabled NAT port map and Hole Punching to increase connectivity in non-public network topologies [#79](https://github.com/ipfs/someguy/pull/79)

## [v0.4.0]

### Changed

- go-libp2p [0.36.1](https://github.com/libp2p/go-libp2p/releases/tag/v0.36.1)
- boxo [0.22.0](https://github.com/ipfs/boxo/releases/tag/v0.22.0)

### Fixed

- [libp2p identify agentVersion](https://github.com/libp2p/specs/blob/master/identify/README.md#agentversion) correctly indicates someguy version

## [v0.3.0]

### Changed

- boxo 0.21 `routing/http` fixes ([release notes](https://github.com/ipfs/boxo/releases/tag/v0.21.0))
- go-libp2p 0.35

### Fixed

- `--version` now includes the release tag
- `start` command supports a graceful shutdown and improved handling of interrupt signals

## [v0.2.3]

### Changed

- The resource manager's defaults have been improved based on Rainbow's and Kubo's defaults. In addition, you can now customize a few options using flags, or [environment variables](./docs/environment-variables.md).

## [v0.2.2]

### Fixed

- The `/routing/v1/peers` endpoint correctly filters out private addresses.

## [v0.2.1]

### Fixed

- Upgraded Boxo with fix to ensure that `/routing/v1/peers` endpoint accepts all variants of Peer IDs that are seen in the wild.
