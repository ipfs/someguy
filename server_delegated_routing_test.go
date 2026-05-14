package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/routing/http/server"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectEndpoints(t *testing.T) {
	t.Run("deduplicates same URL across multiple endpoint types", func(t *testing.T) {
		cfg := &config{
			contentEndpoints: []string{"https://example.com"},
			peerEndpoints:    []string{"https://example.com"},
			ipnsEndpoints:    []string{"https://example.com"},
		}

		endpoints := collectEndpoints(cfg)

		require.Len(t, endpoints, 1, "should have exactly one endpoint")
		assert.Equal(t, "https://example.com", endpoints[0].baseURL)
		assert.True(t, endpoints[0].providers, "should support providers")
		assert.True(t, endpoints[0].peers, "should support peers")
		assert.True(t, endpoints[0].ipns, "should support ipns")
	})

	t.Run("handles different URLs separately", func(t *testing.T) {
		cfg := &config{
			contentEndpoints: []string{"https://a.com", "https://b.com"},
			peerEndpoints:    []string{"https://b.com", "https://c.com"},
		}

		endpoints := collectEndpoints(cfg)

		require.Len(t, endpoints, 3, "should have three separate endpoints")

		// Convert to map for easier testing
		urlMap := make(map[string]endpointConfig)
		for _, ep := range endpoints {
			urlMap[ep.baseURL] = ep
		}

		// Verify a.com (providers only)
		assert.True(t, urlMap["https://a.com"].providers)
		assert.False(t, urlMap["https://a.com"].peers)
		assert.False(t, urlMap["https://a.com"].ipns)

		// Verify b.com (providers and peers)
		assert.True(t, urlMap["https://b.com"].providers)
		assert.True(t, urlMap["https://b.com"].peers)
		assert.False(t, urlMap["https://b.com"].ipns)

		// Verify c.com (peers only)
		assert.False(t, urlMap["https://c.com"].providers)
		assert.True(t, urlMap["https://c.com"].peers)
		assert.False(t, urlMap["https://c.com"].ipns)
	})

	t.Run("skips empty strings", func(t *testing.T) {
		cfg := &config{
			contentEndpoints: []string{"https://example.com", "", "https://another.com"},
			peerEndpoints:    []string{""},
		}

		endpoints := collectEndpoints(cfg)

		require.Len(t, endpoints, 2, "should skip empty strings")

		urlMap := make(map[string]endpointConfig)
		for _, ep := range endpoints {
			urlMap[ep.baseURL] = ep
		}

		assert.Contains(t, urlMap, "https://example.com")
		assert.Contains(t, urlMap, "https://another.com")
		assert.NotContains(t, urlMap, "")
	})

	t.Run("handles all three endpoint types for different URLs", func(t *testing.T) {
		cfg := &config{
			contentEndpoints: []string{"https://provider.com"},
			peerEndpoints:    []string{"https://peer.com"},
			ipnsEndpoints:    []string{"https://ipns.com"},
		}

		endpoints := collectEndpoints(cfg)

		require.Len(t, endpoints, 3)

		urlMap := make(map[string]endpointConfig)
		for _, ep := range endpoints {
			urlMap[ep.baseURL] = ep
		}

		// Each URL should have only one capability enabled
		assert.True(t, urlMap["https://provider.com"].providers)
		assert.False(t, urlMap["https://provider.com"].peers)
		assert.False(t, urlMap["https://provider.com"].ipns)

		assert.False(t, urlMap["https://peer.com"].providers)
		assert.True(t, urlMap["https://peer.com"].peers)
		assert.False(t, urlMap["https://peer.com"].ipns)

		assert.False(t, urlMap["https://ipns.com"].providers)
		assert.False(t, urlMap["https://ipns.com"].peers)
		assert.True(t, urlMap["https://ipns.com"].ipns)
	})

	t.Run("empty config returns empty list", func(t *testing.T) {
		cfg := &config{}

		endpoints := collectEndpoints(cfg)

		assert.Empty(t, endpoints)
	})
}

// providersRouterFunc adapts a function to the router interface, returning
// records only for FindProviders so other methods of composableRouter do
// not need a full mock for these focused tests.
type providersRouterFunc func(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error)

func (f providersRouterFunc) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	return f(ctx, key, limit)
}
func (f providersRouterFunc) FindPeers(context.Context, peer.ID, int) (iter.ResultIter[*types.PeerRecord], error) {
	return nil, fmt.Errorf("not implemented")
}
func (f providersRouterFunc) GetClosestPeers(context.Context, cid.Cid) (iter.ResultIter[*types.PeerRecord], error) {
	return nil, fmt.Errorf("not implemented")
}
func (f providersRouterFunc) GetIPNS(context.Context, ipns.Name) (*ipns.Record, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f providersRouterFunc) PutIPNS(context.Context, ipns.Name, *ipns.Record) error {
	return fmt.Errorf("not implemented")
}

func TestProvidersLimitsHonorSpecCap(t *testing.T) {
	t.Parallel()

	// Supply more records than either cap so both the JSON and NDJSON
	// assertions prove their cap is the binding limit, not the mock size.
	supplied := DefaultStreamingRecordsLimit + 500
	require.Greater(t, supplied, DefaultRecordsLimit, "mock must exceed both caps")
	makeRecords := func(t *testing.T) []iter.Result[types.Record] {
		recs := make([]iter.Result[types.Record], 0, supplied)
		for i := range supplied {
			_, p := makeEd25519PeerID(t)
			ma, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/10.0.0.%d/tcp/4001", (i%254)+1))
			require.NoError(t, err)
			recs = append(recs, iter.Result[types.Record]{
				Val: &types.PeerRecord{
					Schema: types.SchemaPeer,
					ID:     &p,
					Addrs:  []types.Multiaddr{{Multiaddr: ma}},
				},
			})
		}
		return recs
	}

	// Mock router honors the limit like a real DHT router: it stops
	// emitting records after producing `limit` of them. The boxo server
	// passes the configured limit down as a hint, which is what the spec
	// asks the server to do.
	makeHandler := func(records []iter.Result[types.Record], gotLimit *int) http.Handler {
		var providers router = providersRouterFunc(func(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
			if gotLimit != nil {
				*gotLimit = limit
			}
			if limit > 0 && limit < len(records) {
				return iter.FromSlice(records[:limit]), nil
			}
			return iter.FromSlice(records), nil
		})
		return server.Handler(
			&composableRouter{providers: providers},
			server.WithRecordsLimit(DefaultRecordsLimit),
			server.WithStreamingRecordsLimit(DefaultStreamingRecordsLimit),
		)
	}

	// Use a real CID so the path parsing inside the boxo server accepts it.
	c, err := cid.Decode("bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi")
	require.NoError(t, err)

	t.Run("JSON honors spec cap of 100", func(t *testing.T) {
		t.Parallel()
		var gotLimit int
		srv := httptest.NewServer(makeHandler(makeRecords(t), &gotLimit))
		t.Cleanup(srv.Close)

		req, err := http.NewRequest(http.MethodGet, srv.URL+"/routing/v1/providers/"+c.String(), nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		// Count records by their opening token. The boxo JSON serializer
		// emits each provider record as `{"Addrs": ...}`.
		got := strings.Count(string(body), `{"Addrs":`)
		require.Equal(t, DefaultRecordsLimit, got,
			"JSON path should cap at DefaultRecordsLimit (HTTP routing v1 spec section 4.1.5)")
		require.Equal(t, DefaultRecordsLimit, gotLimit,
			"the limit hint passed to the underlying router must be DefaultRecordsLimit")
	})

	t.Run("NDJSON caps at DefaultStreamingRecordsLimit", func(t *testing.T) {
		t.Parallel()
		var gotLimit int
		srv := httptest.NewServer(makeHandler(makeRecords(t), &gotLimit))
		t.Cleanup(srv.Close)

		req, err := http.NewRequest(http.MethodGet, srv.URL+"/routing/v1/providers/"+c.String(), nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/x-ndjson")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/x-ndjson", resp.Header.Get("Content-Type"))

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var count int
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) != "" {
				count++
			}
		}
		require.NoError(t, scanner.Err())
		require.Equal(t, DefaultStreamingRecordsLimit, count,
			"NDJSON should cap at DefaultStreamingRecordsLimit when more records are available")
		require.Equal(t, DefaultStreamingRecordsLimit, gotLimit,
			"the limit hint passed to the underlying router must be DefaultStreamingRecordsLimit")
	})
}
