# 🤷 Someguy

[![Official Part of IPFS Project](https://img.shields.io/badge/project-IPFS-blue.svg?style=flat-square)](https://ipfs.tech)
[![Discourse Forum](https://img.shields.io/discourse/posts?server=https%3A%2F%2Fdiscuss.ipfs.tech)](https://discuss.ipfs.tech)
[![Matrix](https://img.shields.io/matrix/ipfs-space%3Aipfs.io?server_fqdn=matrix.org)](https://matrix.to/#/#ipfs-space:ipfs.io)
[![CI](https://img.shields.io/github/actions/workflow/status/ipfs/someguy/go-test.yml?branch=main)](https://github.com/ipfs/someguy/actions)
[![Code Coverage](https://codecov.io/gh/ipfs/someguy/branch/main/graph/badge.svg?token=9eG7d8fbCB)](https://codecov.io/gh/ipfs/someguy)
[![GitHub Release](https://img.shields.io/github/v/release/ipfs/someguy?filter=!*rc*)](https://github.com/ipfs/someguy/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/ipfs/someguy.svg)](https://pkg.go.dev/github.com/ipfs/someguy)

A [Delegated Routing V1](https://specs.ipfs.tech/routing/http-routing-v1/) server and proxy for all your routing needs. Ask `someguy` for directions.

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
$ docker run --rm -it --net=host -e ghcr.io/ipfs/someguy:main-latest
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

## Deployment

Suggested method for self-hosting is to run a [prebuilt Docker image](#docker).

## Release

To make a release, create a new PR that updates the [`version.json`](./version.json) file. This PR should not include any other changes besides the version bump. A new release will be automatically made once the PR is merged

## License

Dual-licensed under [MIT + Apache 2.0](LICENSE.md)
