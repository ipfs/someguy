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

	t.Run("trailing slashes handled consistently", func(t *testing.T) {
		cfg := config{
			autoConf: autoConfConfig{
				enabled: true,
			},
			dhtType: "none",
			contentEndpoints: []string{
				"https://example.com/routing/v1/providers/", // with trailing slash
				"https://another.com/routing/v1/providers",  // without trailing slash
				"https://base.com/",                         // base URL with trailing slash
				"https://clean.com",                         // base URL without trailing slash
			},
		}

		mockAutoConf := &autoconf.Config{}
		err := expandDelegatedRoutingEndpoints(&cfg, mockAutoConf)
		require.NoError(t, err)

		// Verify trailing slashes are normalized (removed) consistently
		// Paths should be stripped and trailing slashes removed
		assert.ElementsMatch(t, []string{
			"https://another.com",
			"https://base.com", // trailing slash removed
			"https://clean.com",
			"https://example.com", // path stripped and trailing slash removed
		}, cfg.contentEndpoints)
	})
}

// TestNormalizeEndpointURL verifies URL normalization and path handling
func TestNormalizeEndpointURL(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		expectedPath string
		flagName     string
		want         string
		wantErr      bool
		errContains  string
	}{
		{
			name:         "auto placeholder passes through",
			url:          autoconf.AutoPlaceholder,
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			want:         autoconf.AutoPlaceholder,
			wantErr:      false,
		},
		{
			name:         "URL with expected path stripped",
			url:          "https://example.com/routing/v1/providers",
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			want:         "https://example.com",
			wantErr:      false,
		},
		{
			name:         "URL with trailing slash and expected path passes through",
			url:          "https://example.com/routing/v1/providers/",
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			want:         "https://example.com/routing/v1/providers/",
			wantErr:      false,
		},
		{
			name:         "base URL without path",
			url:          "https://example.com",
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			want:         "https://example.com",
			wantErr:      false,
		},
		{
			name:         "base URL with trailing slash",
			url:          "https://example.com/",
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			want:         "https://example.com/",
			wantErr:      false,
		},
		{
			name:         "peers path in provider flag errors with mismatch message",
			url:          "https://example.com/routing/v1/peers",
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			want:         "",
			wantErr:      true,
			errContains:  "has path \"/routing/v1/peers\" which doesn't match --provider-endpoints (expected \"/routing/v1/providers\"",
		},
		{
			name:         "IPNS path in provider flag errors with mismatch message",
			url:          "https://example.com/routing/v1/ipns",
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			want:         "",
			wantErr:      true,
			errContains:  "has path \"/routing/v1/ipns\" which doesn't match --provider-endpoints (expected \"/routing/v1/providers\"",
		},
		{
			name:         "provider path in peer flag errors with mismatch message",
			url:          "https://example.com/routing/v1/providers",
			expectedPath: autoconf.RoutingV1PeersPath,
			flagName:     "--peer-endpoints",
			want:         "",
			wantErr:      true,
			errContains:  "has path \"/routing/v1/providers\" which doesn't match --peer-endpoints (expected \"/routing/v1/peers\"",
		},
		{
			name:         "provider path in IPNS flag errors with mismatch message",
			url:          "https://example.com/routing/v1/providers",
			expectedPath: autoconf.RoutingV1IPNSPath,
			flagName:     "--ipns-endpoints",
			want:         "",
			wantErr:      true,
			errContains:  "has path \"/routing/v1/providers\" which doesn't match --ipns-endpoints (expected \"/routing/v1/ipns\"",
		},
		{
			name:         "peer path in IPNS flag errors with mismatch message",
			url:          "https://example.com/routing/v1/peers",
			expectedPath: autoconf.RoutingV1IPNSPath,
			flagName:     "--ipns-endpoints",
			want:         "",
			wantErr:      true,
			errContains:  "has path \"/routing/v1/peers\" which doesn't match --ipns-endpoints (expected \"/routing/v1/ipns\"",
		},
		{
			name:         "IPNS path in peer flag errors with mismatch message",
			url:          "https://example.com/routing/v1/ipns",
			expectedPath: autoconf.RoutingV1PeersPath,
			flagName:     "--peer-endpoints",
			want:         "",
			wantErr:      true,
			errContains:  "has path \"/routing/v1/ipns\" which doesn't match --peer-endpoints (expected \"/routing/v1/peers\"",
		},
		{
			name:         "custom path accepted",
			url:          "https://example.com/custom/path",
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			want:         "https://example.com/custom/path",
			wantErr:      false,
		},
		{
			name:         "empty URL passes through",
			url:          "",
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			want:         "",
			wantErr:      false,
		},
		{
			name:         "peer path works for peer endpoints",
			url:          "https://example.com/routing/v1/peers",
			expectedPath: autoconf.RoutingV1PeersPath,
			flagName:     "--peer-endpoints",
			want:         "https://example.com",
			wantErr:      false,
		},
		{
			name:         "IPNS path works for IPNS endpoints",
			url:          "https://example.com/routing/v1/ipns",
			expectedPath: autoconf.RoutingV1IPNSPath,
			flagName:     "--ipns-endpoints",
			want:         "https://example.com",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeEndpointURL(tt.url, tt.expectedPath, tt.flagName)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// TestValidateEndpointURLs verifies batch URL validation with proper error messages
func TestValidateEndpointURLs(t *testing.T) {
	tests := []struct {
		name         string
		urls         []string
		expectedPath string
		flagName     string
		envVar       string
		want         []string
		wantErr      bool
		errContains  []string
	}{
		{
			name:         "valid URLs pass validation",
			urls:         []string{"https://a.com", "https://b.com/routing/v1/providers"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			envVar:       "SOMEGUY_PROVIDER_ENDPOINTS",
			want:         []string{"https://a.com", "https://b.com"},
			wantErr:      false,
		},
		{
			name:         "auto placeholder passes validation",
			urls:         []string{autoconf.AutoPlaceholder},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			envVar:       "SOMEGUY_PROVIDER_ENDPOINTS",
			want:         []string{autoconf.AutoPlaceholder},
			wantErr:      false,
		},
		{
			name:         "mixed auto and custom URLs",
			urls:         []string{autoconf.AutoPlaceholder, "https://custom.com"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			envVar:       "SOMEGUY_PROVIDER_ENDPOINTS",
			want:         []string{autoconf.AutoPlaceholder, "https://custom.com"},
			wantErr:      false,
		},
		{
			name:         "mismatched path error includes flag and env var",
			urls:         []string{"https://example.com/routing/v1/peers"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			envVar:       "SOMEGUY_PROVIDER_ENDPOINTS",
			want:         nil,
			wantErr:      true,
			errContains:  []string{"SOMEGUY_PROVIDER_ENDPOINTS", "--provider-endpoints", "/routing/v1/peers"},
		},
		{
			name:         "empty URLs list",
			urls:         []string{},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			envVar:       "SOMEGUY_PROVIDER_ENDPOINTS",
			want:         []string{},
			wantErr:      false,
		},
		{
			name:         "URLs with trailing slashes pass through",
			urls:         []string{"https://a.com/routing/v1/providers/", "https://b.com/"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			flagName:     "--provider-endpoints",
			envVar:       "SOMEGUY_PROVIDER_ENDPOINTS",
			want:         []string{"https://a.com/routing/v1/providers/", "https://b.com/"},
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateEndpointURLs(tt.urls, tt.expectedPath, tt.flagName, tt.envVar)
			if tt.wantErr {
				require.Error(t, err)
				for _, contains := range tt.errContains {
					assert.Contains(t, err.Error(), contains)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// TestStripRoutingPaths verifies path stripping and deduplication logic
func TestStripRoutingPaths(t *testing.T) {
	tests := []struct {
		name         string
		urls         []string
		expectedPath string
		want         []string
	}{
		{
			name:         "strips expected paths",
			urls:         []string{"https://a.com/routing/v1/providers", "https://b.com/routing/v1/providers"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{"https://a.com", "https://b.com"},
		},
		{
			name:         "normalizes trailing slashes",
			urls:         []string{"https://a.com/routing/v1/providers/", "https://b.com/routing/v1/providers"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{"https://a.com", "https://b.com"},
		},
		{
			name:         "preserves base URLs without paths",
			urls:         []string{"https://a.com", "https://b.com"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{"https://a.com", "https://b.com"},
		},
		{
			name:         "preserves custom paths",
			urls:         []string{"https://a.com/custom/path", "https://b.com"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{"https://a.com/custom/path", "https://b.com"},
		},
		{
			name:         "deduplicates after stripping",
			urls:         []string{"https://a.com/routing/v1/providers", "https://a.com", "https://a.com/routing/v1/providers"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{"https://a.com"},
		},
		{
			name:         "mixed URLs with and without paths",
			urls:         []string{"https://a.com/routing/v1/providers", "https://b.com", "https://c.com/custom"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{"https://a.com", "https://b.com", "https://c.com/custom"},
		},
		{
			name:         "removes trailing slashes from base URLs",
			urls:         []string{"https://a.com/", "https://b.com"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{"https://a.com", "https://b.com"},
		},
		{
			name:         "deduplicates URLs with and without trailing slashes",
			urls:         []string{"https://a.com/", "https://a.com", "https://b.com/"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{"https://a.com", "https://b.com"},
		},
		{
			name:         "empty URLs list",
			urls:         []string{},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{},
		},
		{
			name:         "single URL with path",
			urls:         []string{"https://example.com/routing/v1/providers"},
			expectedPath: autoconf.RoutingV1ProvidersPath,
			want:         []string{"https://example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripRoutingPaths(tt.urls, tt.expectedPath)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDeduplicateEndpoints verifies deduplication and sorting behavior
func TestDeduplicateEndpoints(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "empty slice",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "no duplicates",
			input: []string{"https://a.com", "https://b.com"},
			want:  []string{"https://a.com", "https://b.com"},
		},
		{
			name:  "with duplicates",
			input: []string{"https://a.com", "https://b.com", "https://a.com"},
			want:  []string{"https://a.com", "https://b.com"},
		},
		{
			name:  "all duplicates",
			input: []string{"https://a.com", "https://a.com", "https://a.com"},
			want:  []string{"https://a.com"},
		},
		{
			name:  "unsorted input gets sorted",
			input: []string{"https://c.com", "https://a.com", "https://b.com"},
			want:  []string{"https://a.com", "https://b.com", "https://c.com"},
		},
		{
			name:  "duplicates with unsorted input",
			input: []string{"https://c.com", "https://a.com", "https://b.com", "https://a.com", "https://c.com"},
			want:  []string{"https://a.com", "https://b.com", "https://c.com"},
		},
		{
			name:  "single element",
			input: []string{"https://example.com"},
			want:  []string{"https://example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deduplicateEndpoints(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
