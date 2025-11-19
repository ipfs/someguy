package main

import (
	"testing"

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
