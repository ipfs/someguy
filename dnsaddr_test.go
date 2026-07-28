package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ipfs/boxo/routing/http/server"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	madns "github.com/multiformats/go-multiaddr-dns"
	"github.com/stretchr/testify/require"
)

// stubDNS answers TXT lookups from a fixed table so these tests never touch the
// network. It counts lookups so the cache and the per-request budget can be
// asserted on. A non-zero delay makes every lookup take that long, so tests can
// hold one in flight.
type stubDNS struct {
	txt   map[string][]string
	errs  map[string]error
	delay time.Duration

	// A canceled request can leave several detached lookups running at once,
	// so counting has to be safe to call concurrently.
	mu     sync.Mutex
	counts map[string]int
}

func (s *stubDNS) count(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[name]
}

// distinct is how many different names were looked up.
func (s *stubDNS) distinct() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.counts)
}

func (s *stubDNS) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) { return nil, nil }

func (s *stubDNS) LookupTXT(ctx context.Context, name string) ([]string, error) {
	s.mu.Lock()
	s.counts[name]++
	s.mu.Unlock()

	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err, ok := s.errs[name]; ok {
		return nil, err
	}
	return s.txt[name], nil
}

func newStubResolver(t *testing.T, s *stubDNS) *dnsAddrResolver {
	t.Helper()
	mr, err := madns.NewResolver(madns.WithDefaultResolver(s))
	require.NoError(t, err)
	r, err := newDNSAddrResolver(mr)
	require.NoError(t, err)
	return r
}

const (
	testPeerA = "12D3KooWM8sovaEGU1bmiWGWAzvs47DEcXKZZTuJnpQyVTkRs2Vn"
	testPeerB = "12D3KooWM8sovaEGU1bmiWGWAzvs47DEcXKZZTuJnpQyVTkRs2Vz"
	testCID   = "bafkreifjjcie6lypi6ny7amxnfftagclbuxndqonfipmb64f2km2devei4"
)

func mustAddrs(t *testing.T, ss ...string) []types.Multiaddr {
	t.Helper()
	var out []types.Multiaddr
	for _, s := range ss {
		m, err := ma.NewMultiaddr(s)
		require.NoError(t, err)
		out = append(out, types.Multiaddr{Multiaddr: m})
	}
	return out
}

func addrStrings(addrs []types.Multiaddr) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}

// The zero value has to resolve, because that is the configured default and
// because a mode that silently did nothing would be the wrong way to fail.
func TestDNSAddrResolutionDefault(t *testing.T) {
	t.Parallel()

	mode, err := ParseDNSAddrResolution("append")
	require.NoError(t, err)
	require.Equal(t, DNSAddrResolutionAppend, mode)
	mode, err = ParseDNSAddrResolution("replace")
	require.NoError(t, err)
	require.Equal(t, DNSAddrResolutionReplace, mode)

	// With no filter-addrs on the request: the mode's namesake choice.
	noFilter := t.Context()
	require.Equal(t, dnsAddrAppend, DNSAddrResolutionAppend.action(noFilter),
		"without a filter there is nothing to skew, so keep the dnsaddr and add the resolved addrs")
	require.Equal(t, dnsAddrReplace, DNSAddrResolutionReplace.action(noFilter),
		"the operator chose smaller responses over the client's ability to re-resolve")
	require.Equal(t, dnsAddrSkip, DNSAddrResolutionFiltered.action(noFilter))
	require.Equal(t, dnsAddrSkip, DNSAddrResolutionNever.action(noFilter))

	// With filter-addrs on the request.
	filtered := context.WithValue(t.Context(), addrFilterCtxKey{}, parseAddrFilter("ws"))
	require.Equal(t, dnsAddrReplace, DNSAddrResolutionAppend.action(filtered),
		"a surviving dnsaddr would keep a record alive that the filter meant to drop")
	require.Equal(t, dnsAddrReplace, DNSAddrResolutionReplace.action(filtered))
	require.Equal(t, dnsAddrReplace, DNSAddrResolutionFiltered.action(filtered))
	require.Equal(t, dnsAddrSkip, DNSAddrResolutionNever.action(filtered))

	// filter-addrs=dnsaddr is the one filter a bare /dnsaddr does match, so
	// replace would empty the very response the client asked for.
	dnsaddrOnly := context.WithValue(t.Context(), addrFilterCtxKey{}, parseAddrFilter("dnsaddr"))
	require.Equal(t, dnsAddrAppend, DNSAddrResolutionAppend.action(dnsaddrOnly),
		"keep the dnsaddr; boxo's filter drops the appended resolved addrs")
	require.Equal(t, dnsAddrAppend, DNSAddrResolutionReplace.action(dnsaddrOnly),
		"the dnsaddr filter exception holds in replace mode too")
	require.Equal(t, dnsAddrAppend, DNSAddrResolutionFiltered.action(dnsaddrOnly))

	mixed := context.WithValue(t.Context(), addrFilterCtxKey{}, parseAddrFilter("DNSAddr,ws"))
	require.Equal(t, dnsAddrAppend, DNSAddrResolutionAppend.action(mixed),
		"the filter can match both the dnsaddr and what it resolves to, and boxo lowercases entries")
	require.Equal(t, dnsAddrAppend, DNSAddrResolutionReplace.action(mixed))
	require.Equal(t, dnsAddrAppend, DNSAddrResolutionFiltered.action(mixed))

	negated := context.WithValue(t.Context(), addrFilterCtxKey{}, parseAddrFilter("!dnsaddr"))
	require.Equal(t, dnsAddrReplace, DNSAddrResolutionAppend.action(negated),
		"excluding dnsaddr is exactly what replace does")
	require.Equal(t, dnsAddrReplace, DNSAddrResolutionReplace.action(negated))

	_, err = ParseDNSAddrResolution("nonsense")
	require.Error(t, err)
}

// requestHasAddrFilter reports whether withAddrFilter recorded filter-addrs
// on the request context.
func requestHasAddrFilter(ctx context.Context) bool {
	_, ok := ctx.Value(addrFilterCtxKey{}).(addrFilter)
	return ok
}

// withAddrFilter must record filter-addrs only for the endpoints whose
// responses boxo actually filters: replace mode on any other endpoint would
// cost the client the /dnsaddr indirection with nothing gained.
func TestAddrFilterScope(t *testing.T) {
	t.Parallel()

	var saw bool
	h := withAddrFilter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = requestHasAddrFilter(r.Context())
	}))

	for path, want := range map[string]bool{
		"/routing/v1/providers/x":         true,
		"/routing/v1/peers/x":             true,
		"/routing/v1/dht/closest/peers/x": false,
		"/routing/v1/ipns/x":              false,
	} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path+"?filter-addrs=ws", nil))
		require.Equal(t, want, saw, path)
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/routing/v1/providers/x", nil))
	require.False(t, saw, "no filter-addrs param, nothing to record")
}

func TestDNSAddrResolver(t *testing.T) {
	t.Parallel()

	pidA, err := peer.Decode(testPeerA)
	require.NoError(t, err)

	t.Run("replaces dnsaddr with the addresses it names", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.example.com": {"dnsaddr=/dns4/example.com/tcp/3000/ws/p2p/" + testPeerA},
		}}
		budget := newDNSAddrBudget()
		got := newStubResolver(t, s).resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/example.com"), budget, false)
		// The /p2p component is dropped: the record already carries the ID.
		require.Equal(t, []string{"/dns4/example.com/tcp/3000/ws"}, addrStrings(got))
	})

	t.Run("drops addresses that name a different peer", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.shared.example": {
				"dnsaddr=/dns4/shared.example/tcp/1/ws/p2p/" + testPeerA,
				"dnsaddr=/dns4/shared.example/tcp/2/ws/p2p/" + testPeerB,
			},
		}}
		budget := newDNSAddrBudget()
		got := newStubResolver(t, s).resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/shared.example"), budget, false)
		require.Equal(t, []string{"/dns4/shared.example/tcp/1/ws"}, addrStrings(got))
	})

	t.Run("keeps the dnsaddr when resolution fails", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, errs: map[string]error{
			"_dnsaddr.broken.example": fmt.Errorf("SERVFAIL"),
		}}
		budget := newDNSAddrBudget()
		got := newStubResolver(t, s).resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/broken.example"), budget, false)
		require.Equal(t, []string{"/dnsaddr/broken.example"}, addrStrings(got),
			"a DNS failure must not drop the provider")
	})

	t.Run("follows nested dnsaddr but stops at the recursion limit", func(t *testing.T) {
		t.Parallel()
		txt := map[string][]string{}
		// A chain longer than the limit, ending in a real address that must not be reached.
		for i := 0; i < DNSAddrRecursionLimit+2; i++ {
			txt[fmt.Sprintf("_dnsaddr.hop%d.example", i)] = []string{
				fmt.Sprintf("dnsaddr=/dnsaddr/hop%d.example/p2p/%s", i+1, testPeerA),
			}
		}
		txt[fmt.Sprintf("_dnsaddr.hop%d.example", DNSAddrRecursionLimit+2)] = []string{
			"dnsaddr=/dns4/deep.example/tcp/1/ws/p2p/" + testPeerA,
		}
		s := &stubDNS{counts: map[string]int{}, txt: txt}
		budget := newDNSAddrBudget()
		got := newStubResolver(t, s).resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/hop0.example"), budget, false)
		require.Equal(t, []string{"/dnsaddr/hop0.example"}, addrStrings(got),
			"an over-deep chain resolves to nothing, so the original is kept")
		require.LessOrEqual(t, s.distinct(), DNSAddrRecursionLimit, "recursion must stop at the limit")
	})

	t.Run("caps distinct lookups per request", func(t *testing.T) {
		t.Parallel()
		txt := map[string][]string{}
		var addrs []string
		for i := 0; i < MaxDNSAddrLookupsPerRequest+5; i++ {
			host := fmt.Sprintf("h%d.example", i)
			txt["_dnsaddr."+host] = []string{"dnsaddr=/dns4/" + host + "/tcp/1/ws/p2p/" + testPeerA}
			addrs = append(addrs, "/dnsaddr/"+host)
		}
		s := &stubDNS{counts: map[string]int{}, txt: txt}
		budget := newDNSAddrBudget()
		newStubResolver(t, s).resolveAddrs(t.Context(), pidA, mustAddrs(t, addrs...), budget, false)
		require.Equal(t, MaxDNSAddrLookupsPerRequest, s.distinct(),
			"a single request must not be able to trigger unbounded DNS lookups")
	})

	t.Run("caches so a repeated hostname is looked up once", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.cached.example": {"dnsaddr=/dns4/cached.example/tcp/1/ws/p2p/" + testPeerA},
		}}
		r := newStubResolver(t, s)
		for i := 0; i < 5; i++ {
			budget := newDNSAddrBudget()
			r.resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/cached.example"), budget, false)
		}
		require.Equal(t, 1, s.count("_dnsaddr.cached.example"))
	})

	t.Run("cache hits do not consume the lookup budget", func(t *testing.T) {
		t.Parallel()
		txt := map[string][]string{}
		var addrs []string
		n := MaxDNSAddrLookupsPerRequest + 5
		for i := 0; i < n; i++ {
			host := fmt.Sprintf("warm%d.example", i)
			txt["_dnsaddr."+host] = []string{"dnsaddr=/dns4/" + host + "/tcp/1/ws/p2p/" + testPeerA}
			addrs = append(addrs, "/dnsaddr/"+host)
		}
		s := &stubDNS{counts: map[string]int{}, txt: txt}
		r := newStubResolver(t, s)
		for _, a := range addrs {
			budget := newDNSAddrBudget()
			r.resolveAddrs(t.Context(), pidA, mustAddrs(t, a), budget, false)
		}

		budget := newDNSAddrBudget()
		got := r.resolveAddrs(t.Context(), pidA, mustAddrs(t, addrs...), budget, false)
		require.Len(t, got, n, "every cached name resolves, even past the lookup cap")
		for _, a := range got {
			require.False(t, isDNSAddr(a.Multiaddr), "nothing should be left unresolved: %s", a)
		}
		require.Equal(t, MaxDNSAddrLookupsPerRequest, budget.lookups, "cache hits are free")
	})

	t.Run("shares the lookup budget across all records of one response", func(t *testing.T) {
		t.Parallel()
		txt := map[string][]string{}
		var recs []*types.PeerRecord
		for i := 0; i < MaxDNSAddrLookupsPerRequest+5; i++ {
			host := fmt.Sprintf("rec%d.example", i)
			txt["_dnsaddr."+host] = []string{"dnsaddr=/dns4/" + host + "/tcp/1/ws/p2p/" + testPeerA}
			recs = append(recs, &types.PeerRecord{
				Schema: types.SchemaPeer, ID: &pidA,
				Addrs: mustAddrs(t, "/dnsaddr/"+host),
			})
		}
		s := &stubDNS{counts: map[string]int{}, txt: txt}
		r := withDNSAddrResolution(dnsStubRouter{recs: recs}, newStubResolver(t, s), DNSAddrResolutionAppend)
		it, err := r.FindProviders(t.Context(), cid.MustParse(testCID), 0)
		require.NoError(t, err)
		for it.Next() {
		}
		require.Equal(t, MaxDNSAddrLookupsPerRequest, s.distinct(),
			"the cap is per request, not per record")

		// FindPeers goes through resolvePeerRecords and must share one budget
		// across the response the same way.
		s2 := &stubDNS{counts: map[string]int{}, txt: txt}
		r2 := withDNSAddrResolution(dnsStubRouter{recs: recs}, newStubResolver(t, s2), DNSAddrResolutionAppend)
		it2, err := r2.FindPeers(t.Context(), pidA, 0)
		require.NoError(t, err)
		for it2.Next() {
		}
		require.Equal(t, MaxDNSAddrLookupsPerRequest, s2.distinct())
	})

	t.Run("a request that is already gone starts no lookup", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.healthy.example": {"dnsaddr=/dns4/healthy.example/tcp/1/ws/p2p/" + testPeerA},
		}}
		r := newStubResolver(t, s)

		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		budget := newDNSAddrBudget()
		got := r.resolveAddrs(canceled, pidA, mustAddrs(t, "/dnsaddr/healthy.example"), budget, false)

		require.Equal(t, []string{"/dnsaddr/healthy.example"}, addrStrings(got),
			"nothing resolved, so the indirection is kept")
		require.Zero(t, s.distinct(), "a dead request must not start new DNS queries")
		require.Equal(t, MaxDNSAddrLookupsPerRequest, budget.lookups, "and must not spend budget on them")
	})

	t.Run("a lookup already in flight still fills the cache", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, delay: 50 * time.Millisecond, txt: map[string][]string{
			"_dnsaddr.healthy.example": {"dnsaddr=/dns4/healthy.example/tcp/1/ws/p2p/" + testPeerA},
		}}
		r := newStubResolver(t, s)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan []types.Multiaddr, 1)
		go func() {
			budget := newDNSAddrBudget()
			done <- r.resolveAddrs(ctx, pidA, mustAddrs(t, "/dnsaddr/healthy.example"), budget, false)
		}()

		// Cancel only once the query is actually running, so this exercises the
		// in-flight case rather than the shed-early one above.
		require.Eventually(t, func() bool { return s.count("_dnsaddr.healthy.example") > 0 },
			5*time.Second, time.Millisecond)
		cancel()

		select {
		case got := <-done:
			require.Equal(t, []string{"/dnsaddr/healthy.example"}, addrStrings(got),
				"the caller stops waiting and keeps the indirection")
		case <-time.After(5 * time.Second):
			t.Fatal("resolveAddrs did not return after its request was canceled")
		}

		// The detached query carries on, so the real answer lands in the cache
		// rather than the cancellation being remembered as a failure.
		require.Eventually(t, func() bool {
			entry, ok := r.cache.Get("/dnsaddr/healthy.example")
			return ok && entry.ok && len(entry.addrs) > 0
		}, 5*time.Second, time.Millisecond)

		budget := newDNSAddrBudget()
		got := r.resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/healthy.example"), budget, false)
		require.Equal(t, []string{"/dns4/healthy.example/tcp/1/ws"}, addrStrings(got))
		require.Equal(t, 1, s.count("_dnsaddr.healthy.example"), "the cached answer is reused")
	})

	t.Run("a lookup timeout is retried sooner than a hard failure", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, errs: map[string]error{
			"_dnsaddr.slow.example": context.DeadlineExceeded,
			"_dnsaddr.dead.example": fmt.Errorf("NXDOMAIN"),
		}}
		r := newStubResolver(t, s)
		budget := newDNSAddrBudget()
		r.resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/slow.example", "/dnsaddr/dead.example"), budget, false)

		expiry := func(key string) time.Time {
			entry, ok := r.cache.Get(key)
			require.True(t, ok, key)
			return entry.expires
		}
		require.GreaterOrEqual(t,
			expiry("/dnsaddr/dead.example").Sub(expiry("/dnsaddr/slow.example")),
			DNSAddrFailureCacheTTL-DNSAddrCacheTTL,
			"a slow nameserver is worth retrying sooner than a dead name")
	})

	t.Run("hostname variants share one cache entry", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.varied.example": {"dnsaddr=/dns4/varied.example/tcp/1/ws/p2p/" + testPeerA},
		}}
		r := newStubResolver(t, s)
		// Lowercase first, so the one real DNS query matches the stub table.
		for _, variant := range []string{
			"/dnsaddr/varied.example",
			"/dnsaddr/VARIED.example",
			"/dnsaddr/varied.example/p2p/" + testPeerA,
		} {
			budget := newDNSAddrBudget()
			got := r.resolveAddrs(t.Context(), pidA, mustAddrs(t, variant), budget, false)
			require.Equal(t, []string{"/dns4/varied.example/tcp/1/ws"}, addrStrings(got), variant)
		}
		require.Equal(t, map[string]int{"_dnsaddr.varied.example": 1}, s.counts,
			"case and /p2p-suffix variants must not each query DNS")
	})

	t.Run("an expired cache entry is looked up again", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.expired.example": {"dnsaddr=/dns4/expired.example/tcp/1/ws/p2p/" + testPeerA},
		}}
		r := newStubResolver(t, s)
		r.cache.Add("/dnsaddr/expired.example", dnsAddrCacheEntry{expires: time.Now().Add(-time.Second)})

		budget := newDNSAddrBudget()
		got := r.resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/expired.example"), budget, false)
		require.Equal(t, []string{"/dns4/expired.example/tcp/1/ws"}, addrStrings(got))
		require.Equal(t, 1, s.count("_dnsaddr.expired.example"))
	})

	t.Run("caps how many addresses one record can gain", func(t *testing.T) {
		t.Parallel()
		txt := map[string][]string{}
		fanout := func(host string, n int) {
			var entries []string
			for i := 0; i < n; i++ {
				entries = append(entries, fmt.Sprintf("dnsaddr=/dns4/%s/tcp/%d/ws/p2p/%s", host, i+1, testPeerA))
			}
			txt["_dnsaddr."+host] = entries
		}
		fanout("wide1.example", 60)
		fanout("wide2.example", 60)
		fanout("wide3.example", 60)
		s := &stubDNS{counts: map[string]int{}, txt: txt}
		budget := newDNSAddrBudget()
		got := newStubResolver(t, s).resolveAddrs(t.Context(), pidA,
			mustAddrs(t, "/dnsaddr/wide1.example", "/dnsaddr/wide2.example", "/dnsaddr/wide3.example"), budget, false)
		// All 60 from wide1; 40 from wide2 plus its original, kept because its
		// expansion was truncated; wide3 passed through unresolved.
		require.Len(t, got, MaxDNSAddrResolvedPerRecord+2)
		strs := addrStrings(got)
		require.Contains(t, strs, "/dnsaddr/wide2.example",
			"a truncated expansion keeps the indirection alongside what fit")
		require.Contains(t, strs, "/dnsaddr/wide3.example")
		require.NotContains(t, s.counts, "_dnsaddr.wide3.example",
			"no lookups for a record already at its cap")
	})

	t.Run("drops a dnsaddr whose /p2p names a different peer", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}}
		budget := newDNSAddrBudget()
		got := newStubResolver(t, s).resolveAddrs(t.Context(), pidA,
			mustAddrs(t, "/dnsaddr/other.example/p2p/"+testPeerB), budget, false)
		require.Empty(t, got, "an addr naming another peer cannot yield usable addresses")
		require.Empty(t, s.counts, "and is not worth a lookup")
	})

	t.Run("resolves legacy bitswap records", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.example.com": {"dnsaddr=/dns4/example.com/tcp/3000/ws/p2p/" + testPeerA},
		}}
		//lint:ignore SA1019 // ignore staticcheck
		rec := &types.BitswapRecord{Schema: types.SchemaBitswap, ID: &pidA, Addrs: mustAddrs(t, "/dnsaddr/example.com")}
		r := withDNSAddrResolution(bitswapStubRouter{rec: rec}, newStubResolver(t, s), DNSAddrResolutionAppend)
		it, err := r.FindProviders(t.Context(), cid.MustParse(testCID), 0)
		require.NoError(t, err)
		var got []string
		for it.Next() {
			//lint:ignore SA1019 // ignore staticcheck
			rec, ok := it.Val().Val.(*types.BitswapRecord)
			require.True(t, ok)
			got = append(got, addrStrings(rec.Addrs)...)
		}
		require.ElementsMatch(t, []string{"/dnsaddr/example.com", "/dns4/example.com/tcp/3000/ws"}, got,
			"the legacy schema branch must resolve like SchemaPeer")
	})

	t.Run("concurrent requests share one lookup", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := &stubDNS{counts: map[string]int{}, delay: DNSAddrLookupTimeout / 2, txt: map[string][]string{
				"_dnsaddr.busy.example": {"dnsaddr=/dns4/busy.example/tcp/1/ws/p2p/" + testPeerA},
			}}
			r := newStubResolver(t, s)
			addrs := mustAddrs(t, "/dnsaddr/busy.example")
			results := make(chan []string, 5)
			for i := 0; i < 5; i++ {
				go func() {
					budget := newDNSAddrBudget()
					results <- addrStrings(r.resolveAddrs(t.Context(), pidA, addrs, budget, false))
				}()
			}
			for i := 0; i < 5; i++ {
				require.Equal(t, []string{"/dns4/busy.example/tcp/1/ws"}, <-results)
			}
			require.Equal(t, 1, s.count("_dnsaddr.busy.example"),
				"requests that miss while a lookup is in flight must wait for it, not repeat it")
		})
	})
}

// dnsStubRouter returns fixed records, copied per call so several requests can
// run against one stub.
type dnsStubRouter struct {
	router
	recs []*types.PeerRecord
	// raw is returned verbatim ahead of recs, for schemas someguy has no type
	// for.
	raw []types.Record
}

func (r dnsStubRouter) FindProviders(context.Context, cid.Cid, int) (iter.ResultIter[types.Record], error) {
	out := make([]types.Record, 0, len(r.recs)+len(r.raw))
	out = append(out, r.raw...)
	for _, rec := range r.recs {
		cp := *rec
		out = append(out, &cp)
	}
	return iter.ToResultIter(iter.FromSlice(out)), nil
}

func (r dnsStubRouter) FindPeers(context.Context, peer.ID, int) (iter.ResultIter[*types.PeerRecord], error) {
	out := make([]*types.PeerRecord, 0, len(r.recs))
	for _, rec := range r.recs {
		cp := *rec
		out = append(out, &cp)
	}
	return iter.ToResultIter(iter.FromSlice(out)), nil
}

func (r dnsStubRouter) GetClosestPeers(context.Context, cid.Cid) (iter.ResultIter[*types.PeerRecord], error) {
	out := make([]*types.PeerRecord, 0, len(r.recs))
	for _, rec := range r.recs {
		cp := *rec
		out = append(out, &cp)
	}
	return iter.ToResultIter(iter.FromSlice(out)), nil
}

// bitswapStubRouter returns one legacy bitswap record.
type bitswapStubRouter struct {
	router
	//lint:ignore SA1019 // ignore staticcheck
	rec *types.BitswapRecord
}

func (r bitswapStubRouter) FindProviders(context.Context, cid.Cid, int) (iter.ResultIter[types.Record], error) {
	cp := *r.rec
	return iter.ToResultIter(iter.FromSlice([]types.Record{&cp})), nil
}

// The whole point of resolving is that boxo's filter, which runs after someguy
// hands back a record, can then match on a real transport. This drives the real
// HTTP handler to prove the ordering works end to end.
func TestDNSAddrResolutionThroughHandler(t *testing.T) {
	t.Parallel()

	pid, err := peer.Decode(testPeerA)
	require.NoError(t, err)
	provPath := "/routing/v1/providers/" + testCID

	newServer := func(t *testing.T, mode DNSAddrResolution) *httptest.Server {
		t.Helper()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.example.com": {"dnsaddr=/dns4/example.com/tcp/3000/ws/p2p/" + testPeerA},
		}}
		stub := dnsStubRouter{recs: []*types.PeerRecord{{
			Schema: types.SchemaPeer, ID: &pid,
			Addrs: mustAddrs(t, "/dnsaddr/example.com"),
		}}}
		r := withDNSAddrResolution(stub, newStubResolver(t, s), mode)
		h := server.Handler(&composableRouter{providers: r})
		srv := httptest.NewServer(withAddrFilter(h))
		t.Cleanup(srv.Close)
		return srv
	}

	get := func(t *testing.T, srv *httptest.Server, path string) []string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var body struct {
			Providers []struct {
				Addrs []string
			}
			Peers []struct {
				Addrs []string
			}
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		var out []string
		for _, p := range body.Providers {
			out = append(out, p.Addrs...)
		}
		for _, p := range body.Peers {
			out = append(out, p.Addrs...)
		}
		return out
	}

	t.Run("filtered mode resolves so the ws filter matches", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t, DNSAddrResolutionFiltered)
		require.Equal(t, []string{"/dns4/example.com/tcp/3000/ws"}, get(t, srv, provPath+"?filter-addrs=ws"),
			"a provider reachable over ws must survive filter-addrs=ws")
	})

	t.Run("filtered mode leaves the dnsaddr alone when no filter is sent", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t, DNSAddrResolutionFiltered)
		require.Equal(t, []string{"/dnsaddr/example.com"}, get(t, srv, provPath),
			"an unfiltered request keeps the indirection and costs no DNS lookup")
	})

	t.Run("unfiltered request keeps the dnsaddr and gains the resolved addrs", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t, DNSAddrResolutionAppend)
		require.ElementsMatch(t,
			[]string{"/dnsaddr/example.com", "/dns4/example.com/tcp/3000/ws"},
			get(t, srv, provPath),
			"the client can dial now and still re-resolve later")
	})

	t.Run("replace mode drops the dnsaddr even without a filter", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t, DNSAddrResolutionReplace)
		require.Equal(t, []string{"/dns4/example.com/tcp/3000/ws"}, get(t, srv, provPath),
			"the operator chose smaller responses over the client's ability to re-resolve")
	})

	t.Run("filtered request drops the dnsaddr it cannot match", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t, DNSAddrResolutionAppend)
		require.Equal(t, []string{"/dns4/example.com/tcp/3000/ws"}, get(t, srv, provPath+"?filter-addrs=ws"),
			"keeping the dnsaddr here would let it survive filters it cannot match")
	})

	t.Run("negative filter excludes a peer that really speaks the transport", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t, DNSAddrResolutionFiltered)
		require.Empty(t, get(t, srv, provPath+"?filter-addrs=!ws"),
			"resolution has to fix negative filters too, not just positive ones")
	})

	t.Run("a filter naming dnsaddr keeps the indirection", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.example.com": {"dnsaddr=/dns4/example.com/tcp/3000/ws/p2p/" + testPeerA},
		}}
		stub := dnsStubRouter{recs: []*types.PeerRecord{{
			Schema: types.SchemaPeer, ID: &pid,
			// The second addr names a different peer and must be discarded
			// without costing a lookup.
			Addrs: mustAddrs(t, "/dnsaddr/example.com", "/dnsaddr/other.example/p2p/"+testPeerB),
		}}}
		r := withDNSAddrResolution(stub, newStubResolver(t, s), DNSAddrResolutionAppend)
		srv := httptest.NewServer(withAddrFilter(server.Handler(&composableRouter{providers: r})))
		t.Cleanup(srv.Close)
		require.Equal(t, []string{"/dnsaddr/example.com"}, get(t, srv, provPath+"?filter-addrs=dnsaddr"),
			"this is the one filter a /dnsaddr matches, so replacing would empty the response")
		require.Equal(t, 1, s.distinct(),
			"the kept dnsaddr resolves like any other; only the foreign-peer one costs no lookup")
	})

	t.Run("a filter mixing dnsaddr with a transport gets both", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t, DNSAddrResolutionAppend)
		require.ElementsMatch(t,
			[]string{"/dnsaddr/example.com", "/dns4/example.com/tcp/3000/ws"},
			get(t, srv, provPath+"?filter-addrs=dnsaddr,ws"),
			"the filter can match the indirection and what it resolves to")
	})

	t.Run("excluding dnsaddr replaces it with what it names", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t, DNSAddrResolutionAppend)
		require.Equal(t, []string{"/dns4/example.com/tcp/3000/ws"}, get(t, srv, provPath+"?filter-addrs=!dnsaddr"))
	})

	t.Run("never mode leaves the dnsaddr alone even when filtering", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t, DNSAddrResolutionNever)
		require.Empty(t, get(t, srv, provPath+"?filter-addrs=ws"), "unresolved dnsaddr cannot match ws")
	})

	t.Run("peers endpoint resolves and filters too", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.example.com": {"dnsaddr=/dns4/example.com/tcp/3000/ws/p2p/" + testPeerA},
		}}
		stub := dnsStubRouter{recs: []*types.PeerRecord{{
			Schema: types.SchemaPeer, ID: &pid,
			Addrs: mustAddrs(t, "/dnsaddr/example.com"),
		}}}
		r := withDNSAddrResolution(stub, newStubResolver(t, s), DNSAddrResolutionAppend)
		srv := httptest.NewServer(withAddrFilter(server.Handler(&composableRouter{peers: r})))
		t.Cleanup(srv.Close)
		require.Equal(t, []string{"/dns4/example.com/tcp/3000/ws"},
			get(t, srv, "/routing/v1/peers/"+peer.ToCid(pid).String()+"?filter-addrs=ws"),
			"boxo filters /routing/v1/peers as well, so FindPeers must resolve like FindProviders")
	})

	t.Run("orders addresses ip, dns, dnsaddr, then relay", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.example.com": {"dnsaddr=/dns4/example.com/tcp/3000/ws/p2p/" + testPeerA},
		}}
		relay := "/ip4/9.9.9.9/tcp/4001/p2p/" + testPeerB + "/p2p-circuit"
		stub := dnsStubRouter{recs: []*types.PeerRecord{{
			Schema: types.SchemaPeer, ID: &pid,
			Addrs: mustAddrs(t, relay, "/dnsaddr/example.com", "/ip4/1.2.3.4/tcp/4001"),
		}}}
		r := withDNSAddrResolution(stub, newStubResolver(t, s), DNSAddrResolutionAppend)
		srv := httptest.NewServer(withAddrFilter(server.Handler(&composableRouter{providers: r})))
		t.Cleanup(srv.Close)
		require.Equal(t, []string{
			"/ip4/1.2.3.4/tcp/4001",
			"/dns4/example.com/tcp/3000/ws",
			"/dnsaddr/example.com",
			relay,
		}, get(t, srv, provPath), "a client dialing in order tries the most direct addresses first")
	})

	t.Run("sorts records without any dnsaddr too", func(t *testing.T) {
		t.Parallel()
		relay := "/ip4/9.9.9.9/tcp/4001/p2p/" + testPeerB + "/p2p-circuit"
		stub := dnsStubRouter{recs: []*types.PeerRecord{{
			Schema: types.SchemaPeer, ID: &pid,
			Addrs: mustAddrs(t, relay, "/dns4/example.com/tcp/443/tls/ws", "/ip4/1.2.3.4/tcp/4001"),
		}}}
		r := withDNSAddrResolution(stub, newStubResolver(t, &stubDNS{counts: map[string]int{}}), DNSAddrResolutionAppend)
		srv := httptest.NewServer(withAddrFilter(server.Handler(&composableRouter{providers: r})))
		t.Cleanup(srv.Close)
		require.Equal(t, []string{
			"/ip4/1.2.3.4/tcp/4001",
			"/dns4/example.com/tcp/443/tls/ws",
			relay,
		}, get(t, srv, provPath), "delegated-router records with plain addrs come out ordered as well")
	})

	t.Run("closest peers endpoint resolves but never replaces", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.example.com": {"dnsaddr=/dns4/example.com/tcp/3000/ws/p2p/" + testPeerA},
		}}
		stub := dnsStubRouter{recs: []*types.PeerRecord{{
			Schema: types.SchemaPeer, ID: &pid,
			Addrs: mustAddrs(t, "/dnsaddr/example.com"),
		}}}
		r := withDNSAddrResolution(stub, newStubResolver(t, s), DNSAddrResolutionAppend)
		srv := httptest.NewServer(withAddrFilter(server.Handler(&composableRouter{dht: r})))
		t.Cleanup(srv.Close)
		want := []string{"/dns4/example.com/tcp/3000/ws", "/dnsaddr/example.com"}
		require.Equal(t, want, get(t, srv, "/routing/v1/dht/closest/peers/"+testCID))
		require.Equal(t, want, get(t, srv, "/routing/v1/dht/closest/peers/"+testCID+"?filter-addrs=ws"),
			"boxo never filters this endpoint, so filter-addrs must not cost the client the dnsaddr")
	})
}

// The bounds have to hold against a record built to defeat them, not just
// against well-formed DNS.
func TestDNSAddrResolverBounds(t *testing.T) {
	t.Parallel()

	pidA, err := peer.Decode(testPeerA)
	require.NoError(t, err)

	// A TXT record that lists itself expands as fan^depth unless the output
	// limit is threaded through the recursion. Re-entering a cached name costs
	// no lookup, so a limit applied only to the finished result would still let
	// one request build millions of addresses before anything checked.
	t.Run("a self-referential record cannot fan out", func(t *testing.T) {
		t.Parallel()
		const fan = 40
		var entries []string
		for i := 0; i < fan; i++ {
			entries = append(entries,
				"dnsaddr=/dnsaddr/evil.example/p2p/"+testPeerA,
				fmt.Sprintf("dnsaddr=/ip4/10.0.0.%d/tcp/1/p2p/%s", i, testPeerA))
		}
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{"_dnsaddr.evil.example": entries}}
		r := newStubResolver(t, s)

		budget := newDNSAddrBudget()
		start := time.Now()
		got := r.resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/evil.example"), budget, false)
		elapsed := time.Since(start)

		// +1 because the unresolvable remainder keeps the original /dnsaddr.
		require.LessOrEqual(t, len(got), MaxDNSAddrResolvedPerRecord+1)
		require.Less(t, elapsed, 2*time.Second, "bounded output means bounded work")
		require.Equal(t, 1, s.count("_dnsaddr.evil.example"), "one cached name, one query")
		require.Contains(t, addrStrings(got), "/dnsaddr/evil.example",
			"a truncated expansion keeps the indirection the rest lives behind")
	})

	t.Run("the per-record cap is shared across a record's addresses", func(t *testing.T) {
		t.Parallel()
		txt := map[string][]string{}
		var addrs []string
		for i := 0; i < 4; i++ {
			host := fmt.Sprintf("many%d.example", i)
			var entries []string
			for j := 0; j < 60; j++ {
				entries = append(entries, fmt.Sprintf("dnsaddr=/ip4/10.%d.0.%d/tcp/1/p2p/%s", i, j, testPeerA))
			}
			txt["_dnsaddr."+host] = entries
			addrs = append(addrs, "/dnsaddr/"+host)
		}
		s := &stubDNS{counts: map[string]int{}, txt: txt}
		budget := newDNSAddrBudget()
		got := newStubResolver(t, s).resolveAddrs(t.Context(), pidA, mustAddrs(t, addrs...), budget, false)

		resolved := 0
		for _, a := range got {
			if !isDNSAddr(a.Multiaddr) {
				resolved++
			}
		}
		require.LessOrEqual(t, resolved, MaxDNSAddrResolvedPerRecord,
			"240 available addresses must not all land in one record")
	})

	// Only a bare /dnsaddr/<host> can match a published TXT entry, so anything
	// else must not cost a lookup or a 15 minute empty cache entry.
	t.Run("an unsatisfiable shape costs no lookup", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}}
		r := newStubResolver(t, s)
		relay := "/dnsaddr/relay.example/p2p/" + testPeerB + "/p2p-circuit/p2p/" + testPeerA

		budget := newDNSAddrBudget()
		got := r.resolveAddrs(t.Context(), pidA, mustAddrs(t, relay), budget, false)

		require.Equal(t, []string{relay}, addrStrings(got), "kept as it was")
		require.Zero(t, s.distinct(), "madns could never satisfy this, so do not ask")
		require.Equal(t, MaxDNSAddrLookupsPerRequest, budget.lookups)
	})

	t.Run("a request with no budget left resolves nothing more", func(t *testing.T) {
		t.Parallel()
		s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
			"_dnsaddr.late.example": {"dnsaddr=/dns4/late.example/tcp/1/ws/p2p/" + testPeerA},
		}}
		r := newStubResolver(t, s)

		budget := newDNSAddrBudget()
		budget.lookups = 0
		got := r.resolveAddrs(t.Context(), pidA, mustAddrs(t, "/dnsaddr/late.example"), budget, false)

		require.Equal(t, []string{"/dnsaddr/late.example"}, addrStrings(got))
		require.Zero(t, s.distinct(), "with the budget spent nothing more is looked up")
	})
}

// Normalizing an attacker-controlled name into a shared key namespace is how a
// cache becomes a poisoning primitive, so only the hostname is normalized and
// only over ASCII.
func TestDNSAddrCacheKey(t *testing.T) {
	t.Parallel()

	key := func(s string) string {
		m, err := ma.NewMultiaddr(s)
		require.NoError(t, err)
		return dnsAddrCacheKey(m)
	}

	t.Run("hostname case and trailing dot share one entry", func(t *testing.T) {
		t.Parallel()
		want := key("/dnsaddr/bootstrap.libp2p.io")
		require.Equal(t, want, key("/dnsaddr/Bootstrap.LibP2P.io"))
		require.Equal(t, want, key("/dnsaddr/bootstrap.libp2p.io."))
	})

	t.Run("a unicode lookalike does not fold onto an ascii name", func(t *testing.T) {
		t.Parallel()
		// strings.ToLower maps U+0130 to 'i' and U+212A to 'k', which would let
		// these share the victim's cache entry and its 15 minute failure TTL.
		require.NotEqual(t, key("/dnsaddr/bootstrap.libp2p.io"), key("/dnsaddr/bootstrap.lİbp2p.io"))
		require.NotEqual(t, key("/dnsaddr/kbootstrap.io"), key("/dnsaddr/Kbootstrap.io"))
	})

	t.Run("case-sensitive components after the host are preserved", func(t *testing.T) {
		t.Parallel()
		a := "/dnsaddr/wt.example/p2p/" + testPeerA
		b := "/dnsaddr/wt.example/p2p/" + testPeerB
		require.NotEqual(t, key(a), key(b), "distinct peer IDs must not share a key")
		require.Contains(t, key(a), testPeerA, "the peer ID keeps its case")
	})
}

// The spec expects unknown schemas to survive a proxy: someguy is not the only
// thing that may understand a record. Every layer that switches on schema
// (sanitizeRouter, dnsAddrRouter, boxo's filter) has to fall through, and a
// switch is easy to grow a case that swallows the rest.
func TestUnknownSchemaPassesThrough(t *testing.T) {
	t.Parallel()

	const raw = `{"Schema":"some-future-thing","Addrs":["/dnsaddr/example.com"],"Extra":{"k":"v"}}`
	var unknown types.UnknownRecord
	require.NoError(t, unknown.UnmarshalJSON([]byte(raw)))
	require.Equal(t, "some-future-thing", unknown.GetSchema())

	s := &stubDNS{counts: map[string]int{}, txt: map[string][]string{
		"_dnsaddr.example.com": {"dnsaddr=/dns4/example.com/tcp/3000/ws/p2p/" + testPeerA},
	}}
	stub := dnsStubRouter{raw: []types.Record{&unknown}}
	r := withDNSAddrResolution(stub, newStubResolver(t, s), DNSAddrResolutionAppend)
	srv := httptest.NewServer(withAddrFilter(server.Handler(&composableRouter{providers: r})))
	t.Cleanup(srv.Close)

	for _, query := range []string{"", "?filter-addrs=ws"} {
		url := fmt.Sprintf("%s/routing/v1/providers/%s%s",
			srv.URL, "bafkreifjjcie6lypi6ny7amxnfftagclbuxndqonfipmb64f2km2devei4", query)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)

		var got struct {
			Providers []map[string]any
		}
		require.NoError(t, json.Unmarshal(body, &got))
		require.Len(t, got.Providers, 1, "query %q dropped the record", query)
		require.Equal(t, "some-future-thing", got.Providers[0]["Schema"])
		require.Equal(t, map[string]any{"k": "v"}, got.Providers[0]["Extra"],
			"fields someguy does not understand must survive")
		require.Equal(t, []any{"/dnsaddr/example.com"}, got.Providers[0]["Addrs"],
			"someguy must not reach into a schema it does not know, even to resolve")
	}
	require.Zero(t, s.distinct(), "an unknown schema costs no DNS lookup")
}

// A multiaddr may carry protocols someguy has no handling for. It must survive
// intact: not dropped, not reordered ahead of things a client should try first,
// and never rewritten. Nothing here is special-cased anywhere in someguy, which
// is the point.
func TestUnknownMultiaddrProtocolPassesThrough(t *testing.T) {
	t.Parallel()

	const (
		onion  = "/onion3/vww6ybal4bd7szmgncyruucpgfkqahzddi37ktceo3ah7ngmcopnpyyd:1234"
		memory = "/memory/42"
		ip     = "/ip4/1.2.3.4/tcp/4001"
	)

	pid, err := peer.Decode(testPeerA)
	require.NoError(t, err)

	s := &stubDNS{counts: map[string]int{}}
	stub := dnsStubRouter{recs: []*types.PeerRecord{{
		Schema: types.SchemaPeer, ID: &pid,
		Addrs: mustAddrs(t, onion, memory, ip),
	}}}
	r := withDNSAddrResolution(stub, newStubResolver(t, s), DNSAddrResolutionAppend)
	srv := httptest.NewServer(withAddrFilter(server.Handler(&composableRouter{providers: r})))
	t.Cleanup(srv.Close)

	get := func(query string) []string {
		t.Helper()
		url := fmt.Sprintf("%s/routing/v1/providers/%s%s",
			srv.URL, "bafkreifjjcie6lypi6ny7amxnfftagclbuxndqonfipmb64f2km2devei4", query)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var body struct {
			Providers []struct{ Addrs []string }
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Len(t, body.Providers, 1)
		return body.Providers[0].Addrs
	}

	t.Run("survives unchanged and sorts behind directly dialable addresses", func(t *testing.T) {
		require.Equal(t, []string{ip, memory, onion}, get(""),
			"unknown protocols are kept verbatim, ranked after ones someguy can place")
	})

	t.Run("a filter naming the unknown protocol matches it", func(t *testing.T) {
		require.Equal(t, []string{onion}, get("?filter-addrs=onion3"),
			"filtering is by protocol component, so it works without someguy knowing the protocol")
	})

	t.Run("a filter not naming it excludes it", func(t *testing.T) {
		require.Equal(t, []string{ip}, get("?filter-addrs=tcp"))
	})

	require.Zero(t, s.distinct(), "no address here is a /dnsaddr, so nothing is looked up")
}
