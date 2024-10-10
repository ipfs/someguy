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

### Changed

### Removed

### Fixed

### Security

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
