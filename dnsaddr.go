package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	madns "github.com/multiformats/go-multiaddr-dns"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/sync/singleflight"
)

const (
	// DNSAddrRecursionLimit bounds how many times a /dnsaddr may point at
	// another /dnsaddr. It matches go-libp2p's dial path, which stops at the
	// same depth.
	DNSAddrRecursionLimit = 4

	// DNSAddrLookupTimeout bounds one TXT lookup. Resolution runs while the
	// response is streaming, so this is the delay a single unreachable
	// nameserver adds to the first byte a client sees. A healthy lookup takes
	// tens of milliseconds. Together with MaxDNSAddrLookupsPerRequest it also
	// bounds the total time one request can spend blocked on lookups.
	DNSAddrLookupTimeout = time.Second

	// DNSAddrCacheTTL is how long a resolved set is reused. madns discards the
	// DNS TTL, so this is a fixed value rather than the record's own. Observed
	// dnsaddr TXT TTLs are 300-600s, and this matches the max-age someguy puts
	// on a response with results.
	DNSAddrCacheTTL = 5 * time.Minute

	// DNSAddrFailureCacheTTL is how long a failed lookup is remembered. It is
	// longer than the success TTL so a hostname that does not resolve is not
	// retried on every request. A lookup that merely timed out uses
	// DNSAddrCacheTTL instead: a slow nameserver is worth retrying sooner than
	// a name that actively fails to resolve.
	DNSAddrFailureCacheTTL = 15 * time.Minute

	// DNSAddrCacheSize caps distinct hostnames remembered. Honest traffic uses
	// a handful; the size exists so records naming many hostnames cannot grow
	// the cache without bound.
	DNSAddrCacheSize = 4096

	// MaxDNSAddrLookupsPerRequest caps how many DNS lookups one request may
	// trigger. Provider records are published by anyone, so without this a
	// single request could name thousands of hostnames and turn someguy into a
	// relay for DNS floods. Cached names are free and do not count against it.
	// Past the cap the address passes through unresolved.
	MaxDNSAddrLookupsPerRequest = 16

	// MaxDNSAddrResolvedPerRecord caps how many addresses resolution may add to
	// one record. It is threaded through the recursion rather than applied to
	// the finished result: a TXT record that lists itself expands as
	// fan^DNSAddrRecursionLimit, and re-entering an already-cached name costs
	// no lookup, so a limit checked only at the end would still let one cheap
	// request build millions of addresses first. go-libp2p bounds the same
	// expansion the same way, in ResolveDNSAddr's outputLimit.
	MaxDNSAddrResolvedPerRecord = 100
)

var (
	dnsAddrResolutions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "dnsaddr_resolutions",
		Subsystem: "routers",
		Namespace: name,
		Help:      "Outcomes of /dnsaddr resolution; resolved/empty/failed count DNS queries, shared by concurrent requests",
	}, []string{"result"})

	dnsAddrResolutionDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:      "dnsaddr_resolution_duration_seconds",
		Subsystem: "routers",
		Namespace: name,
		Help:      "Duration of /dnsaddr DNS queries, one sample per query",
		Buckets:   []float64{0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2},
	})
)

const (
	dnsAddrResultHit       = "cache-hit"
	dnsAddrResultResolved  = "resolved"
	dnsAddrResultEmpty     = "empty"
	dnsAddrResultFailed    = "failed"
	dnsAddrResultThrottled = "throttled"
)

// dnsAddrBudget is the number of DNS lookups one /routing/v1 request may still
// spend on resolution. It bounds the DNS traffic one request can cause, and,
// because each lookup blocks the streaming response for at most
// DNSAddrLookupTimeout, the delay resolution can add to the response.
type dnsAddrBudget struct {
	lookups int
}

func newDNSAddrBudget() *dnsAddrBudget {
	return &dnsAddrBudget{lookups: MaxDNSAddrLookupsPerRequest}
}

// spend reports whether another DNS query is allowed, consuming one if so.
func (b *dnsAddrBudget) spend() bool {
	if b.lookups <= 0 {
		dnsAddrResolutions.WithLabelValues(dnsAddrResultThrottled).Inc()
		return false
	}
	b.lookups--
	return true
}

type dnsAddrCacheEntry struct {
	addrs []ma.Multiaddr
	// ok is false for a lookup that failed, so a cached failure keeps reporting
	// the expansion as incomplete instead of looking like an empty success.
	ok      bool
	expires time.Time
}

// dnsAddrResolver turns /dnsaddr multiaddrs into the addresses they name.
//
// A /dnsaddr carries no transport component, so an address filter such as
// filter-addrs=ws can neither match it nor exclude it, and a provider reachable
// only through a /dnsaddr is dropped from a filtered response while a provider
// the client asked to exclude survives one. Resolving before the filter runs
// fixes both directions. See docs/dnsaddr-resolution.md.
type dnsAddrResolver struct {
	resolver *madns.Resolver

	// flight dedupes concurrent lookups of one name: requests that miss the
	// cache while a lookup for the same name is in flight wait for its result
	// instead of querying DNS again.
	flight singleflight.Group

	cache *lru.Cache[string, dnsAddrCacheEntry]
}

func newDNSAddrResolver(resolver *madns.Resolver) (*dnsAddrResolver, error) {
	cache, err := lru.New[string, dnsAddrCacheEntry](DNSAddrCacheSize)
	if err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = madns.DefaultResolver
	}
	return &dnsAddrResolver{resolver: resolver, cache: cache}, nil
}

// asciiLower lowercases ASCII letters and leaves every other byte alone.
//
// strings.ToLower applies Unicode simple case mapping, which maps a few
// non-ASCII runes onto ASCII: U+0130 to 'i' and U+212A to 'k'. Using it on a
// hostname would let "lİbp2p.io" share a cache entry with "libp2p.io", so one
// record could cache a failure under another's name.
func asciiLower(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			if b == nil {
				b = []byte(s)
			}
			b[i] = c + ('a' - 'A')
		}
	}
	if b == nil {
		return s
	}
	return string(b)
}

// dnsAddrCacheKey normalizes addr into a cache and singleflight key.
//
// Only the hostname is normalized: DNS names are case-insensitive and a
// trailing dot names the same zone, so those variants must share one entry.
// Everything after the host is kept verbatim, because components such as a
// certhash or a peer ID are case-sensitive and folding them would serve one
// address the resolution of a different one.
func dnsAddrCacheKey(addr ma.Multiaddr) string {
	first, rest := ma.SplitFirst(addr)
	if first == nil || first.Protocol().Code != ma.P_DNSADDR {
		return addr.String()
	}
	key := "/dnsaddr/" + strings.TrimSuffix(asciiLower(first.Value()), ".")
	if len(rest) > 0 {
		key += rest.String()
	}
	return key
}

// resolvableDNSAddr reports whether addr is a bare /dnsaddr/<host>, the only
// shape a TXT lookup can satisfy.
//
// madns matches published entries against whatever follows the host in the
// queried address, and real entries end in /p2p/<id>. An address carrying
// anything else, a /p2p-circuit suffix for instance, can only ever resolve to
// nothing, so recognizing it here saves a lookup and avoids caching an empty
// result for it.
func resolvableDNSAddr(addr ma.Multiaddr) bool {
	protos := addr.Protocols()
	return len(protos) == 1 && protos[0].Code == ma.P_DNSADDR
}

// lookup resolves one /dnsaddr, reading through the cache. It spends budget
// only when the cache cannot answer, so a cached name is free. ok is false
// whenever the answer is not the full truth: the request spent its lookup
// budget, is gone, or DNS failed. Callers use that to decide whether to keep
// the original /dnsaddr.
func (d *dnsAddrResolver) lookup(ctx context.Context, addr ma.Multiaddr, budget *dnsAddrBudget) (addrs []ma.Multiaddr, ok bool) {
	key := dnsAddrCacheKey(addr)

	if entry, found := d.cache.Get(key); found && time.Now().Before(entry.expires) {
		dnsAddrResolutions.WithLabelValues(dnsAddrResultHit).Inc()
		return entry.addrs, entry.ok
	}

	// Check before spending: once the response this is for is gone, starting
	// more queries only adds detached DNS traffic nothing will read.
	if ctx.Err() != nil {
		return nil, false
	}
	if !budget.spend() {
		return nil, false
	}

	// The query runs detached from the request context, deduped with other
	// requests asking for the same name while it is in flight. A client that
	// disconnects mid-lookup can therefore neither cache its cancellation as a
	// resolution failure nor waste the answer.
	ch := d.flight.DoChan(key, func() (any, error) {
		return d.resolve(context.WithoutCancel(ctx), addr, key), nil
	})

	select {
	case res := <-ch:
		entry, _ := res.Val.(dnsAddrCacheEntry)
		return entry.addrs, entry.ok
	case <-ctx.Done():
		// The response this lookup was for is gone. The query keeps running so
		// the answer still lands in the cache, but this expansion is not whole.
		return nil, false
	}
}

// resolve queries DNS and caches the outcome. ctx must not carry the request's
// cancellation: a canceled request caching its own cancellation as a failure
// would suppress resolution for every client for DNSAddrFailureCacheTTL.
func (d *dnsAddrResolver) resolve(ctx context.Context, addr ma.Multiaddr, key string) dnsAddrCacheEntry {
	ctx, cancel := context.WithTimeout(ctx, DNSAddrLookupTimeout)
	defer cancel()

	start := time.Now()
	resolved, err := d.resolver.Resolve(ctx, addr)
	dnsAddrResolutionDuration.Observe(time.Since(start).Seconds())

	entry := dnsAddrCacheEntry{addrs: resolved, ok: true}
	ttl := DNSAddrCacheTTL
	switch {
	case err != nil:
		logger.Debugw("dnsaddr resolution failed", "addr", key, "err", err)
		dnsAddrResolutions.WithLabelValues(dnsAddrResultFailed).Inc()
		entry = dnsAddrCacheEntry{ok: false}
		// With cancellation severed above, DeadlineExceeded can only be our own
		// lookup timeout: a slow nameserver, retried sooner than a name that
		// actively fails to resolve.
		if !errors.Is(err, context.DeadlineExceeded) {
			ttl = DNSAddrFailureCacheTTL
		}
	case len(resolved) == 0:
		dnsAddrResolutions.WithLabelValues(dnsAddrResultEmpty).Inc()
		ttl = DNSAddrFailureCacheTTL
	default:
		dnsAddrResolutions.WithLabelValues(dnsAddrResultResolved).Inc()
	}

	entry.expires = time.Now().Add(ttl)
	d.cache.Add(key, entry)
	return entry
}

// resolveAddrs expands every /dnsaddr in addrs into what it resolves to.
//
// keepOriginal decides whether the /dnsaddr survives alongside its resolved
// addresses. It must not when the request carries an address filter that
// cannot match a /dnsaddr: a surviving one would keep a record alive that the
// client asked to exclude. (DNSAddrResolution.action decides, including the
// exception for a filter that names dnsaddr itself.) Without a filter there is
// nothing to skew, and keeping it lets the client re-resolve later while still
// having addresses it can dial now.
//
// An address whose expansion is not whole keeps its original, so a DNS outage,
// a throttled lookup, or a truncated expansion all degrade to the previous
// behavior instead of dropping the indirection the missing addresses live
// behind.
func (d *dnsAddrResolver) resolveAddrs(ctx context.Context, pid peer.ID, addrs []types.Multiaddr, budget *dnsAddrBudget, keepOriginal bool) []types.Multiaddr {
	if !containsDNSAddr(addrs) {
		return addrs
	}

	out := make([]types.Multiaddr, 0, len(addrs))
	seen := make(map[string]struct{}, len(addrs))
	room := MaxDNSAddrResolvedPerRecord

	for _, addr := range addrs {
		if addr.Multiaddr == nil || !isDNSAddr(addr.Multiaddr) {
			appendUnique(&out, seen, addr.Multiaddr)
			continue
		}
		if id, _ := splitPeerID(addr.Multiaddr); id != "" && id != pid {
			// Names a different peer: nothing it yields can belong to this
			// record, so it is dropped and never looked up.
			continue
		}

		resolved, complete := d.expand(ctx, pid, addr.Multiaddr, budget, &room, DNSAddrRecursionLimit)
		for _, r := range resolved {
			appendUnique(&out, seen, r)
		}
		if keepOriginal || !complete || len(resolved) == 0 {
			appendUnique(&out, seen, addr.Multiaddr)
		}
	}

	return out
}

// expand resolves one /dnsaddr, following nested /dnsaddr results up to depth.
//
// room is the number of addresses this record may still gain, shared across the
// whole recursion so a self-referential TXT record cannot fan out. complete is
// false when any part of the expansion was skipped: over the recursion limit,
// out of room, out of request budget, or a failed lookup.
func (d *dnsAddrResolver) expand(ctx context.Context, pid peer.ID, addr ma.Multiaddr, budget *dnsAddrBudget, room *int, depth int) (out []ma.Multiaddr, complete bool) {
	if depth <= 0 || *room <= 0 {
		return nil, false
	}
	// Re-entering a cached name costs no lookup, so without this a request that
	// is already over could keep expanding for free.
	if ctx.Err() != nil {
		return nil, false
	}

	// Strip a trailing /p2p before looking up: DNS never sees it, so keeping it
	// would give one hostname a cache entry per variant. resolveAddrs already
	// dropped addrs naming a different peer.
	_, bare := splitPeerID(addr)
	if !resolvableDNSAddr(bare) {
		return nil, false
	}

	results, ok := d.lookup(ctx, bare, budget)
	complete = ok
	for _, r := range results {
		if *room <= 0 {
			return out, false
		}
		// A dnsaddr TXT record can list addresses for several peers, and the
		// lookup above queries the bare hostname, so discard anything that
		// names a different peer here.
		id, rest := splitPeerID(r)
		if id != "" && id != pid {
			continue
		}
		if len(rest) == 0 {
			continue
		}
		if isDNSAddr(rest) {
			nested, nestedComplete := d.expand(ctx, pid, rest, budget, room, depth-1)
			complete = complete && nestedComplete
			out = append(out, nested...)
			continue
		}
		out = append(out, rest)
		*room--
	}
	return out, complete
}

// splitPeerID returns the /p2p component of addr, if any, and addr without it.
// The peer ID is dropped because the record already carries it in its own ID
// field, and every other address someguy returns omits it.
func splitPeerID(addr ma.Multiaddr) (peer.ID, ma.Multiaddr) {
	rest, last := ma.SplitLast(addr)
	if last == nil || last.Protocol().Code != ma.P_P2P {
		return "", addr
	}
	id, err := peer.Decode(last.Value())
	if err != nil {
		return "", rest
	}
	return id, rest
}

func isDNSAddr(addr ma.Multiaddr) bool {
	for _, p := range addr.Protocols() {
		if p.Code == ma.P_DNSADDR {
			return true
		}
	}
	return false
}

func containsDNSAddr(addrs []types.Multiaddr) bool {
	for _, a := range addrs {
		if a.Multiaddr != nil && isDNSAddr(a.Multiaddr) {
			return true
		}
	}
	return false
}

// appendUnique appends addr to out unless it is empty or already present, and
// reports whether it appended.
func appendUnique(out *[]types.Multiaddr, seen map[string]struct{}, addr ma.Multiaddr) bool {
	if len(addr) == 0 {
		return false
	}
	k := addr.String()
	if _, dup := seen[k]; dup {
		return false
	}
	seen[k] = struct{}{}
	*out = append(*out, types.Multiaddr{Multiaddr: addr})
	return true
}

// DNSAddrResolution controls when someguy resolves a /dnsaddr and what an
// unfiltered response does with the original: each mode is named after that.
// A request that sends filter-addrs always gets the /dnsaddr replaced in the
// resolving modes, because a filter cannot match one; see addrFilter.action
// for the one filter value that is the exception.
type DNSAddrResolution string

const (
	// DNSAddrResolutionAppend resolves on every request, so every client sees
	// the same addresses and none of them has to resolve a /dnsaddr itself.
	// An unfiltered response gains the resolved addresses and keeps the
	// /dnsaddr, so the client can dial now and re-resolve later.
	DNSAddrResolutionAppend DNSAddrResolution = "append"
	// DNSAddrResolutionReplace resolves like append, but an unfiltered
	// response also has the /dnsaddr replaced: smaller responses, at the cost
	// of the indirection the client could have re-resolved later.
	DNSAddrResolutionReplace DNSAddrResolution = "replace"
	// DNSAddrResolutionFiltered resolves only when the request carries
	// filter-addrs. An unfiltered response keeps the /dnsaddr, so that client
	// keeps the indirection and can re-resolve when it dials.
	DNSAddrResolutionFiltered DNSAddrResolution = "filtered"
	// DNSAddrResolutionNever disables resolution.
	DNSAddrResolutionNever DNSAddrResolution = "never"
)

func ParseDNSAddrResolution(s string) (DNSAddrResolution, error) {
	switch DNSAddrResolution(s) {
	case DNSAddrResolutionAppend:
		return DNSAddrResolutionAppend, nil
	case DNSAddrResolutionReplace:
		return DNSAddrResolutionReplace, nil
	case DNSAddrResolutionFiltered:
		return DNSAddrResolutionFiltered, nil
	case DNSAddrResolutionNever:
		return DNSAddrResolutionNever, nil
	default:
		return "", fmt.Errorf("invalid dnsaddr resolution mode %q, must be one of [append, replace, filtered, never]", s)
	}
}

type addrFilterCtxKey struct{}

// addrFilter is what a request's filter-addrs means for a /dnsaddr.
type addrFilter struct {
	// matchesDNSAddr is true when a positive entry names dnsaddr itself. That
	// is the one filter a bare /dnsaddr does match, so replacing it would drop
	// the records the client asked for.
	matchesDNSAddr bool
}

// parseAddrFilter reduces a filter-addrs value to what matters for a /dnsaddr.
// It mirrors how boxo parses the same value (lowercase, split on commas, no
// trimming, entries prefixed with "!" negate), so the two sides cannot
// disagree about whether the filter names dnsaddr.
func parseAddrFilter(param string) addrFilter {
	return addrFilter{
		matchesDNSAddr: slices.Contains(strings.Split(strings.ToLower(param), ","), "dnsaddr"),
	}
}

// action is what a request carrying this filter does with a /dnsaddr.
//
// The default is replace: a /dnsaddr matches no transport, so it would
// otherwise survive a filter meant to exclude it. The exception is a positive
// filter naming dnsaddr itself, which does match one, sent by a client asking
// for the indirections; keep the /dnsaddr then, alongside whatever it
// resolves to. When dnsaddr is the only positive entry the lookups are
// wasted, since boxo's filter drops every resolved address, but that filter
// shape is rare and the waste is bounded by the request's budget.
func (f addrFilter) action() dnsAddrAction {
	if f.matchesDNSAddr {
		return dnsAddrAppend
	}
	return dnsAddrReplace
}

// withAddrFilter records the request's filter-addrs so the routers can see it.
//
// The /routing/v1 handler parses filter-addrs and applies it to whatever the
// router returns, without telling the router anything, so someguy reads the
// query itself and passes the value down the request context. That context is
// the one the handler hands to the router, so the value arrives intact.
//
// Only the providers and peers endpoints actually filter; recording the query
// elsewhere (closest peers ignores it) would make the routers replace a
// /dnsaddr on a response nothing filters.
func withAddrFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filtered := strings.HasPrefix(r.URL.Path, "/routing/v1/providers/") ||
			strings.HasPrefix(r.URL.Path, "/routing/v1/peers/")
		if f := r.URL.Query().Get("filter-addrs"); f != "" && filtered {
			r = r.WithContext(context.WithValue(r.Context(), addrFilterCtxKey{}, parseAddrFilter(f)))
		}
		next.ServeHTTP(w, r)
	})
}

// dnsAddrAction is what to do with a /dnsaddr on one request.
type dnsAddrAction int

const (
	// dnsAddrSkip leaves the /dnsaddr alone and does no DNS lookup.
	dnsAddrSkip dnsAddrAction = iota
	// dnsAddrReplace swaps the /dnsaddr for what it resolves to.
	dnsAddrReplace
	// dnsAddrAppend adds the resolved addresses and keeps the /dnsaddr.
	dnsAddrAppend
)

// action decides what this request does with a /dnsaddr.
//
// A request that filters usually replaces, because a /dnsaddr matches no
// transport and would otherwise survive a filter meant to exclude it; see
// addrFilter.action for the one filter that can match it. What an unfiltered
// request does is the mode's namesake choice; the zero value behaves like
// append, the configured default.
func (m DNSAddrResolution) action(ctx context.Context) dnsAddrAction {
	f, filtered := ctx.Value(addrFilterCtxKey{}).(addrFilter)
	switch m {
	case DNSAddrResolutionNever:
		return dnsAddrSkip
	case DNSAddrResolutionFiltered:
		if filtered {
			return f.action()
		}
		return dnsAddrSkip
	case DNSAddrResolutionReplace:
		if filtered {
			return f.action()
		}
		return dnsAddrReplace
	default:
		if filtered {
			return f.action()
		}
		return dnsAddrAppend
	}
}
