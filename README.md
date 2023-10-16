# someguy

A [Delegated Routing V1](https://specs.ipfs.tech/routing/http-routing-v1/) server and client for all your routing needs. Ask `someguy` for directions.

## Install

```bash
go install github.com/ipfs-shipyard/someguy@latest
```


## Build

```bash
go build -o someguy
```

## Usage

You can use `someguy`  as a client or server.

`someguy start` runs a Delegated Routing V1 server that proxies requests to the [IPFS Amino DHT](https://blog.ipfs.tech/2023-09-amino-refactoring/) and the [cid.contact](https://cid.contact) indexer node.

If you don't want to run a server yourself, but want to query some other server, you can run `someguy ask` and choose any of the subcommands and ask for a provider, a peer, or even an IPNS record.

For more details run `someguy --help`.
