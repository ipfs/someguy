package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetCombinedRouting(t *testing.T) {
	t.Parallel()

	// Check of the result of get combined routing is a sanitize router.
	v, err := getCombinedRouting(nil, &bundledDHT{}, nil, nil)
	require.NoError(t, err)
	require.IsType(t, sanitizeRouter{}, v)

	v, err = getCombinedRouting([]string{"https://example.com/"}, nil, nil, nil)
	require.NoError(t, err)
	require.IsType(t, parallelRouter{}, v)

	v, err = getCombinedRouting([]string{"https://example.com/"}, &bundledDHT{}, nil, nil)
	require.NoError(t, err)
	require.IsType(t, parallelRouter{}, v)
}
