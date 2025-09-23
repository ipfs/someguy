# 🤷 Someguy

[![Official Part of IPFS Project](https://img.shields.io/badge/project-IPFS-blue.svg?style=flat-square)](https://ipfs.tech)
[![Discourse Forum](https://img.shields.io/discourse/posts?server=https%3A%2F%2Fdiscuss.ipfs.tech)](https://discuss.ipfs.tech)
[![Matrix](https://img.shields.io/matrix/ipfs-space%3Aipfs.io?server_fqdn=matrix.org)](https://matrix.to/#/#ipfs-space:ipfs.io)
[![CI](https://img.shields.io/github/actions/workflow/status/ipfs/someguy/go-test.yml?branch=main)](https://github.com/ipfs/someguy/actions)
[![Code Coverage](https://codecov.io/gh/ipfs/someguy/branch/main/graph/badge.svg?token=9eG7d8fbCB)](https://codecov.io/gh/ipfs/someguy)
[![GitHub Release](https://img.shields.io/github/v/release/ipfs/someguy?filter=!*rc*)](https://github.com/ipfs/someguy/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/ipfs/someguy.svg)](https://pkg.go.dev/github.com/ipfs/someguy)

Someguy is an [HTTP Delegated Routing V1](https://specs.ipfs.tech/routing/http-routing-v1/) server that proxies requests to the [Amino DHT](https://docs.ipfs.tech/concepts/glossary/#amino) and other Delegated Routing servers such as the [Network Indexer](https://cid.contact).

Someguy is also hosted as a [public utility](https://docs.ipfs.tech/concepts/public-utilities/#delegated-routing-endpoint) at `https://delegated-ipfs.dev/routing/v1`.

## Build

```bash
go build -o someguy
```

## Install

```bash
go install github.com/ipfs/someguy@latest
```

### Docker

Automated Docker container releases are available from the [Github container registry](https://github.com/ipfs/someguy/pkgs/container/someguy):

- 🟢 Releases
  - `latest` always points at the latest stable release
  - `vN.N.N` point at a specific [release tag](https://github.com/ipfs/someguy/releases)
- 🟠 Unreleased developer builds
  - `main-latest` always points at the `HEAD` of the `main` branch
  - `main-YYYY-DD-MM-GITSHA` points at a specific commit from the `main` branch
- ⚠️ Experimental, unstable builds
  - `staging-latest` always points at the `HEAD` of the `staging` branch
  - `staging-YYYY-DD-MM-GITSHA` points at a specific commit from the `staging` branch
  - This tag is used by developers for internal testing, not intended for end users

When using Docker, make sure to pass necessary config via `-e`:
```console
$ docker pull ghcr.io/ipfs/someguy:main-latest
$ docker run --rm -it --net=host ghcr.io/ipfs/someguy:main-latest
```

See [`/docs/environment-variables.md`](./docs/environment-variables.md).

## Usage

You can use `someguy` as a client, or as a server.

### Server

You can start the server with `someguy start`. This will, by default, run a Delegated Routing V1 server that proxies requests to the [IPFS Amino DHT](https://blog.ipfs.tech/2023-09-amino-refactoring/) and the [cid.contact](https://cid.contact) indexer (IPNI) node.

For more details run `someguy start --help`.

### Client

If you don't want to run a server yourself, but want to query some other server, you can run `someguy ask` and choose any of the subcommands and ask for a provider, a peer, or even an IPNS record.

For more details run `someguy ask --help`.

### AutoConf

Someguy supports automatic configuration of bootstrap peers and delegated routing endpoints through the autoconf feature. When enabled (default), Someguy will fetch configuration from a remote URL and automatically expand the special `auto` placeholder value with network-appropriate defaults.

This feature can be configured via:
- `--autoconf` / `SOMEGUY_AUTOCONF`: Enable/disable autoconf (default: `true`)
- `--autoconf-url` / `SOMEGUY_AUTOCONF_URL`: URL to fetch configuration from (default: `https://conf.ipfs-mainnet.org/autoconf.json`)
- `--autoconf-refresh` / `SOMEGUY_AUTOCONF_REFRESH`: How often to refresh configuration (default: `24h`)

The `auto` placeholder can be used in:
- `--endpoing` / `SOMEGUY_DELEGATED_ENDPOINT`: the Delegated Routing V1 endpoint to ask

**Note:** When autoconf is disabled (`--autoconf=false`), using the `auto` placeholder will cause an error. You must provide explicit values for these configurations when autoconf is disabled.

## Deployment

Suggested method for self-hosting is to run a [prebuilt Docker image](#docker).

## Release

1. Create a PR from branch `release-vX.Y.Z` against `main` that:
   1. Tidies the [`CHANGELOG.md`](CHANGELOG.md) with the changes for the current release
   2. Updates the  [`version.json`](./version.json) file
2. Once the release checker creates a draft release, copy-paste the changelog into the draft
3. Merge the PR, the release will be automatically created once the PR is merged

## License

Dual-licensed under [MIT + Apache 2.0](LICENSE.md)
