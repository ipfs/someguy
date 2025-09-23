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

// TestExpandContentEndpoints verifies endpoint expansion behavior when autoconf is disabled
func TestExpandContentEndpoints(t *testing.T) {
	cfg := config{
		autoConf: autoConfConfig{
			enabled: false,
		},
		contentEndpoints: []string{autoconf.AutoPlaceholder},
	}

	t.Run("auto placeholder errors when autoconf disabled", func(t *testing.T) {
		err := expandContentEndpoints(&cfg, nil)
		require.Error(t, err, "should error when 'auto' is used with autoconf disabled")
		assert.Contains(t, err.Error(), "'auto' placeholder found in endpoint option", "error should mention bootstrap peers")
		assert.Contains(t, err.Error(), "SOMEGUY_DELEGATED_ENDPOINT", "error should mention how to fix")
	})

	t.Run("custom endpoint preserved when autoconf disabled", func(t *testing.T) {
		custom := []string{"https://example.com"}
		cfg.contentEndpoints = custom
		err := expandContentEndpoints(&cfg, nil)
		require.NoError(t, err, "custom endpoint should not error")
		assert.Equal(t, custom, cfg.contentEndpoints, "custom endpoint should be preserved")
	})

	t.Run("mixed auto and custom errors when autoconf disabled", func(t *testing.T) {
		cfg.contentEndpoints = []string{autoconf.AutoPlaceholder, "https://example.com"}
		err := expandContentEndpoints(&cfg, nil)
		require.Error(t, err, "should error when 'auto' is mixed with custom values")
	})
}
