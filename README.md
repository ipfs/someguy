# someguy

A [Delegated Routing V1](https://specs.ipfs.tech/routing/http-routing-v1/) server and proxy for all your routing needs. Ask `someguy` for directions.

## Install

```bash
go install github.com/ipfs/someguy@latest
```

## Build

```bash
go build -o someguy
```

## Usage

You can use `someguy`  as a client (e.g. IPNI caching proxy) or server.

By default, `someguy start` runs a Delegated Routing V1 server that proxies requests to the [IPFS Amino DHT](https://blog.ipfs.tech/2023-09-amino-refactoring/) and the [cid.contact](https://cid.contact) indexer (IPNI) node.

If you don't want to run a server yourself, but want to query some other server, you can run `someguy ask` and choose any of the subcommands and ask for a provider, a peer, or even an IPNS record.

For more details run `someguy --help`.

## Release

To make a release, create a new PR that updates the [`version.json`](./version.json) file. This PR should not include any other changes besides the version bump. A new release will be automatically made once the PR is merged.
