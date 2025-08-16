# Making IPFS More Accessible: How PR #90 Improved Someguy's Performance

## What is Someguy and Why Does It Matter?

Someguy serves as an HTTP gateway to the IPFS network, translating the Amino DHT (Distributed Hash Table) into HTTP responses that browsers and mobile devices can understand. While IPFS uses its own protocols for peer and content discovery, browsers and mobile apps can't speak these protocols directly due to platform limitations and security restrictions.

Instead, these clients can perform "one-way" peer-to-peer operations—they can read content from IPFS peers once they know where to find them. But first, they need someone to tell them which peers have the content they're looking for. That's Someguy's job.

**The process works like this**:
1. Browser/mobile app asks Someguy: "Who has this content?"
2. Someguy queries the IPFS DHT and responds: "These peers have it"
3. Browser/mobile app connects directly to those peers to fetch the content

**The performance equation is straightforward**: the faster Someguy can respond with working peer addresses, the quicker browsers and mobile apps can start fetching content peer-to-peer. Every millisecond saved in routing queries directly translates to faster content delivery.

## The Problem: Dead Ends for Mobile and Browser Clients

Before PR #90, Someguy often gave browsers and mobile apps peer addresses that didn't work. When clients asked "Who has this content?", they'd get responses like:

- Peer A at address X (but address X was outdated)
- Peer B at address Y (but Peer B was offline)
- Peer C with no address at all

This forced mobile apps and browsers to:
- Try multiple failed connections before finding working peers
- Make additional HTTP requests back to Someguy for more peer information
- Fall back to centralized HTTP gateways when peer-to-peer failed
- Show slower loading times to users

For devices with limited battery and bandwidth, these failed connection attempts were particularly costly.

## The Solution: Fresh Peer Information for Mobile and Browser Clients

PR #90 introduces a caching system that ensures browsers and mobile apps get working peer addresses on the first try.

### 1. Cached Address Book: Always Fresh Peer Locations

The new cached address book (`cached_addr_book.go`) maintains current peer information:

- **48-hour cache**: Stores peer addresses long enough to be useful, short enough to stay fresh
- **1M peer capacity**: Can track locations for millions of IPFS peers
- **Memory-efficient**: Uses LRU eviction to keep the most relevant peers readily available
- **Connection-aware**: Tracks which peers are currently online vs recently seen

### 2. Active Peer Probing: Testing Connections Before Serving Them

Rather than serving stale addresses to mobile and browser clients, Someguy now tests peer connectivity in the background:

- **Background verification**: Every 15 minutes, tests whether cached peer addresses still work
- **Exponential backoff**: Stops wasting time on persistently offline peers (1h → 2h → 4h → 48h delays)
- **Concurrent testing**: Tests up to 20 peer connections simultaneously
- **Selective probing**: Only tests peers that haven't been verified recently

### 3. Cached Router: Better Responses for HTTP Clients

The `cachedRouter` (`server_cached_router.go`) improves what browsers and mobile apps receive:

1. **Cache-first responses**: Returns verified peer addresses immediately when available
2. **Background resolution**: If no cached addresses exist, looks up fresh ones without blocking the response
3. **Streaming results**: Sends working peer addresses as soon as they're found
4. **Fallback handling**: Omits peers that can't be reached rather than sending bad addresses

## Impact for Mobile and Browser Applications

### Faster Peer-to-Peer Content Loading
- **Immediate connections**: Browsers and mobile apps can connect to peers on the first try
- **Reduced timeouts**: No more waiting for failed connection attempts
- **Battery savings**: Mobile devices spend less time on unsuccessful network operations

### Better User Experience
- **Faster IPFS websites**: Browsers can load IPFS content more quickly through working peer connections
- **Responsive mobile apps**: Apps using IPFS for data storage see improved performance
- **Reliable peer discovery**: Consistent access to fresh peer information

### Practical Deployment Benefits
- **CDN compatibility**: Consistent 48h cache times work well with HTTP caching layers
- **Reduced server load**: Fewer retry requests from clients getting bad peer information
- **Scalable architecture**: One Someguy instance can serve thousands of browser/mobile clients

## Configuration for Different Client Needs

```bash
# Standard deployment for browser/mobile clients
SOMEGUY_CACHED_ADDR_BOOK=true

# High-availability deployment - longer cache times
SOMEGUY_CACHED_ADDR_BOOK_RECENT_TTL=72h

# Development/testing - disable background probing
SOMEGUY_CACHED_ADDR_BOOK_ACTIVE_PROBING=false
```

## Monitoring Client Success

PR #90 includes metrics to track how well the system serves mobile and browser clients:

- **`someguy_cached_router_peer_addr_lookups`**: How often clients get cached vs fresh peer addresses
- **`someguy_cached_addr_book_probed_peers`**: Ratio of online vs offline peers in the network
- **`someguy_cached_addr_book_probe_duration_seconds`**: How long it takes to verify peer connectivity

## Enabling IPFS for Web and Mobile

This enhancement makes one-way peer-to-peer practical for mainstream applications:

- **Web browsers** can reliably fetch IPFS content by connecting directly to peers
- **Mobile apps** can use IPFS for data storage without maintaining DHT connections
- **Progressive web apps** can offer peer-to-peer functionality without complex networking code
- **Hybrid architectures** can fall back from centralized to peer-to-peer seamlessly

By ensuring that HTTP clients get working peer addresses, PR #90 removes a major friction point in bringing peer-to-peer networking to browsers and mobile devices. The result is faster, more reliable access to decentralized content for billions of HTTP-capable devices.