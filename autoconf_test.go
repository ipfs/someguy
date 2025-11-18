package main

import (
	"testing"

	autoconf "github.com/ipfs/boxo/autoconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetNativeSystems verifies that routing types are correctly mapped to native systems
func TestGetNativeSystems(t *testing.T) {
	tests := []struct {
		name            string
		routingType     string
		expectedSystems []string
	}{
		{
			name:            "DHT routing",
			routingType:     "dht",
			expectedSystems: []string{autoconf.SystemAminoDHT},
		},
		{
			name:            "Accelerated routing",
			routingType:     "accelerated",
			expectedSystems: []string{autoconf.SystemAminoDHT},
		},
		{
			name:            "Standard routing",
			routingType:     "standard",
			expectedSystems: []string{autoconf.SystemAminoDHT},
		},
		{
			name:            "Auto routing",
			routingType:     "auto",
			expectedSystems: []string{autoconf.SystemAminoDHT},
		},
		{
			name:            "Off routing",
			routingType:     "off",
			expectedSystems: []string{},
		},
		{
			name:            "None routing",
			routingType:     "none",
			expectedSystems: []string{},
		},
		{
			name:            "Unknown routing type",
			routingType:     "custom",
			expectedSystems: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			systems := getNativeSystems(tt.routingType)
			assert.Equal(t, tt.expectedSystems, systems)
		})
	}
}

// TestExpandDelegatedRoutingEndpoints verifies endpoint expansion and path categorization
func TestExpandDelegatedRoutingEndpoints(t *testing.T) {
	t.Run("auto placeholder errors when autoconf disabled", func(t *testing.T) {
		cfg := config{
			autoConf: autoConfConfig{
				enabled: false,
			},
			contentEndpoints: []string{autoconf.AutoPlaceholder},
		}
		err := expandDelegatedRoutingEndpoints(&cfg, nil)
		require.Error(t, err, "should error when 'auto' is used with autoconf disabled")
		assert.Contains(t, err.Error(), "'auto' placeholder found in endpoint option")
		assert.Contains(t, err.Error(), "SOMEGUY_PROVIDER_ENDPOINTS")
	})

	t.Run("custom endpoints without paths preserved", func(t *testing.T) {
		cfg := config{
			autoConf: autoConfConfig{
				enabled: false,
			},
			contentEndpoints: []string{"https://example.com"},
		}
		err := expandDelegatedRoutingEndpoints(&cfg, nil)
		require.NoError(t, err)
		// Without autoconf, endpoints pass through unchanged
		assert.Equal(t, []string{"https://example.com"}, cfg.contentEndpoints)
	})

	t.Run("separate flags with matching paths", func(t *testing.T) {
		cfg := config{
			autoConf: autoConfConfig{
				enabled: true,
			},
			dhtType: "none", // no native systems to exclude
			contentEndpoints: []string{
				"https://provider-only.example.com/routing/v1/providers",
				"https://all-in-one.example.com/routing/v1/providers",
			},
			peerEndpoints: []string{
				"https://peer-only.example.com/routing/v1/peers",
				"https://all-in-one.example.com/routing/v1/peers",
			},
			ipnsEndpoints: []string{
				"https://ipns-only.example.com/routing/v1/ipns",
				"https://all-in-one.example.com/routing/v1/ipns",
			},
		}

		mockAutoConf := &autoconf.Config{}
		err := expandDelegatedRoutingEndpoints(&cfg, mockAutoConf)
		require.NoError(t, err)

		// Verify paths stripped to base URLs
		assert.ElementsMatch(t, []string{
			"https://all-in-one.example.com",
			"https://provider-only.example.com",
		}, cfg.contentEndpoints, "provider endpoints should have paths stripped")

		assert.ElementsMatch(t, []string{
			"https://all-in-one.example.com",
			"https://peer-only.example.com",
		}, cfg.peerEndpoints, "peer endpoints should have paths stripped")

		assert.ElementsMatch(t, []string{
			"https://all-in-one.example.com",
			"https://ipns-only.example.com",
		}, cfg.ipnsEndpoints, "IPNS endpoints should have paths stripped")
	})

	t.Run("base URLs and unknown paths accepted", func(t *testing.T) {
		cfg := config{
			autoConf: autoConfConfig{
				enabled: true,
			},
			dhtType: "none",
			contentEndpoints: []string{
				"https://example.com/routing/v1/providers",
				"https://example.com/custom/path",
				"https://example.com",
			},
		}

		mockAutoConf := &autoconf.Config{}
		err := expandDelegatedRoutingEndpoints(&cfg, mockAutoConf)
		require.NoError(t, err)

		// All URLs accepted: known path stripped, unknown path and base URL kept
		assert.ElementsMatch(t, []string{
			"https://example.com",
			"https://example.com/custom/path",
		}, cfg.contentEndpoints)
		assert.Empty(t, cfg.peerEndpoints)
		assert.Empty(t, cfg.ipnsEndpoints)
	})

	t.Run("deduplication works", func(t *testing.T) {
		cfg := config{
			autoConf: autoConfConfig{
				enabled: true,
			},
			dhtType: "none",
			contentEndpoints: []string{
				"https://example.com/routing/v1/providers",
				"https://example.com/routing/v1/providers", // duplicate
				"https://example.com",                      // duplicate after path stripping
			},
			peerEndpoints: []string{
				"https://example.com/routing/v1/peers",
				"https://example.com", // duplicate after path stripping
			},
		}

		mockAutoConf := &autoconf.Config{}
		err := expandDelegatedRoutingEndpoints(&cfg, mockAutoConf)
		require.NoError(t, err)

		// Duplicates removed, paths stripped
		assert.Equal(t, []string{"https://example.com"}, cfg.contentEndpoints)
		assert.Equal(t, []string{"https://example.com"}, cfg.peerEndpoints)
	})

	t.Run("mismatched path errors", func(t *testing.T) {
		cfg := config{
			autoConf: autoConfConfig{
				enabled: true,
			},
			dhtType: "none",
			contentEndpoints: []string{
				"https://example.com/routing/v1/peers", // wrong path for provider endpoints
			},
		}

		mockAutoConf := &autoconf.Config{}
		err := expandDelegatedRoutingEndpoints(&cfg, mockAutoConf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "/routing/v1/peers")
		assert.Contains(t, err.Error(), "--provider-endpoints")
	})

	t.Run("mixing auto with custom URLs", func(t *testing.T) {
		cfg := config{
			autoConf: autoConfConfig{
				enabled: true,
			},
			dhtType: "none",
			contentEndpoints: []string{
				autoconf.AutoPlaceholder,
				"https://custom-provider.example.com",
			},
			peerEndpoints: []string{
				"https://custom-peer.example.com",
			},
			ipnsEndpoints: []string{
				autoconf.AutoPlaceholder,
			},
		}

		// Empty autoConf means "auto" expands to nothing, custom URLs preserved
		mockAutoConf := &autoconf.Config{}
		err := expandDelegatedRoutingEndpoints(&cfg, mockAutoConf)
		require.NoError(t, err)

		// Custom URLs should be preserved
		assert.Equal(t, []string{"https://custom-provider.example.com"}, cfg.contentEndpoints)
		assert.Equal(t, []string{"https://custom-peer.example.com"}, cfg.peerEndpoints)
		assert.Empty(t, cfg.ipnsEndpoints) // auto expanded to nothing
	})

	t.Run("multiple custom URLs in one flag", func(t *testing.T) {
		cfg := config{
			autoConf: autoConfConfig{
				enabled: true,
			},
			dhtType: "none",
			contentEndpoints: []string{
				"https://a.example.com",
				"https://b.example.com/routing/v1/providers",
				"https://c.example.com",
			},
			peerEndpoints: []string{
				"https://peer1.example.com/routing/v1/peers",
				"https://peer2.example.com",
			},
		}

		mockAutoConf := &autoconf.Config{}
		err := expandDelegatedRoutingEndpoints(&cfg, mockAutoConf)
		require.NoError(t, err)

		// All URLs should be processed (paths stripped where present)
		assert.ElementsMatch(t, []string{
			"https://a.example.com",
			"https://b.example.com",
			"https://c.example.com",
		}, cfg.contentEndpoints)

		assert.ElementsMatch(t, []string{
			"https://peer1.example.com",
			"https://peer2.example.com",
		}, cfg.peerEndpoints)
	})
}
