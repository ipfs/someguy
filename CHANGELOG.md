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
