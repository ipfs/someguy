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

- boxo 0.21
- go-libp2p 0.35

### Removed

### Fixed

- `--version` now includes the release tag
- `start` command supports a graceful shutdown and improved handling of interrupt signals

### Security

## [v0.2.3]

### Changed

- The resource manager's defaults have been improved based on Rainbow's and Kubo's defaults. In addition, you can now customize a few options using flags, or [environment variables](./docs/environment-variables.md).

## [v0.2.2]

### Fixed

- The `/routing/v1/peers` endpoint correctly filters out private addresses.

## [v0.2.1]

### Fixed

- Upgraded Boxo with fix to ensure that `/routing/v1/peers` endpoint accepts all variants of Peer IDs that are seen in the wild.
