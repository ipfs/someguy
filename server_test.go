package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCombineRouters(t *testing.T) {
	t.Parallel()

	// Mock router for testing
	mockRouter := composableRouter{}

	// Check that combineRouters with DHT only returns sanitizeRouter
	v := combineRouters(nil, &bundledDHT{}, nil, nil, nil)
	require.IsType(t, sanitizeRouter{}, v)

	// Check that combineRouters with delegated routers only returns parallelRouter
	v = combineRouters(nil, nil, nil, []router{mockRouter}, nil)
	require.IsType(t, parallelRouter{}, v)

	// Check that combineRouters with both DHT and delegated routers returns parallelRouter
	v = combineRouters(nil, &bundledDHT{}, nil, []router{mockRouter}, nil)
	require.IsType(t, parallelRouter{}, v)
}
