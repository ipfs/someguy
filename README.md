# someguy

> ⚠️ Reframe has been deprecated in favour of the [Routing V1 Specification](https://specs.ipfs.tech/routing/http-routing-v1/).

A Reframe server you can delegate routing requests to. Ask someguy for directions.

## Usage

`someguy start` runs a [Reframe](https://github.com/ipfs/specs/blob/6bdb7b2751038e1d0212a40494ea8fd4018f384c/REFRAME.md) server
that proxies requests to the IPFS Public DHT and an Indexer node (planned to be upgraded to querying the indexer network more broadly).

If you don't feel like running any routing code yourself, that's ok just ask `someguy` to do it for you.

If you're looking for an implementation of a Reframe client already packaged up and ready to use check out https://github.com/ipfs/go-delegated-routing.

If you're missing tooling in your language you can take a look at the spec to write an HTTP client yourself, 
or if you're up to it add codegeneration for your language into https://github.com/ipld/edelweiss/ to make it easier to maintain
your implementation if/when more methods are added to Reframe.
