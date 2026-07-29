package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/boxo/routing/http/server"
	"github.com/stretchr/testify/require"
)

func TestCombineRouters(t *testing.T) {
	t.Parallel()

	// Mock router for testing
	mockRouter := composableRouter{}

	// Check that combineRouters with DHT only returns sanitizeRouter
	v := combineRouters(nil, &bundledDHT{}, nil, nil, nil, nil, DNSAddrResolutionNever)
	require.IsType(t, sanitizeRouter{}, v)

	// Check that combineRouters with delegated routers only returns parallelRouter
	v = combineRouters(nil, nil, nil, []router{mockRouter}, nil, nil, DNSAddrResolutionNever)
	require.IsType(t, parallelRouter{}, v)

	// Check that combineRouters with both DHT and delegated routers returns parallelRouter
	v = combineRouters(nil, &bundledDHT{}, nil, []router{mockRouter}, nil, nil, DNSAddrResolutionNever)
	require.IsType(t, parallelRouter{}, v)

	// Check that a resolver wraps both branches in dnsAddrRouter
	resolver, err := newDNSAddrResolver(nil)
	require.NoError(t, err)
	v = combineRouters(nil, &bundledDHT{}, nil, nil, nil, resolver, DNSAddrResolutionAppend)
	require.IsType(t, dnsAddrRouter{}, v)
	v = combineRouters(nil, nil, nil, []router{mockRouter}, nil, resolver, DNSAddrResolutionAppend)
	require.IsType(t, dnsAddrRouter{}, v)
}

// A record that resolved early has to reach the client while later records
// are still being looked up. Compression middleware defaults defeat this by
// withholding small writes, so guard the behavior rather than the setting.
func TestCompressedNDJSONFlushesEachRecord(t *testing.T) {
	t.Parallel()

	// Shorter than httpcompression's default MinSize, so a buffering
	// middleware would hold it back.
	const firstRecord = `{"Schema":"peer","ID":"first"}` + "\n"
	const secondRecord = `{"Schema":"peer","ID":"second"}` + "\n"

	secondReady := make(chan struct{})
	handlerDone := make(chan struct{})

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		w.Header().Set("Content-Type", "application/x-ndjson")

		_, err := w.Write([]byte(firstRecord))
		require.NoError(t, err)
		w.(http.Flusher).Flush()

		// Stand in for a provider whose addresses are still being resolved.
		<-secondReady

		_, err = w.Write([]byte(secondRecord))
		require.NoError(t, err)
		w.(http.Flusher).Flush()
	})

	compress, err := newCompressionAdapter()
	require.NoError(t, err)

	srv := httptest.NewServer(compress(h))
	t.Cleanup(srv.Close)

	// Registered after srv.Close so it runs first: a failing assertion must
	// not leave the handler parked while Close waits for it.
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(secondReady) }) }
	t.Cleanup(release)

	// Bounds every step below. A buffering middleware withholds the response
	// headers too, so without this the request itself would block forever.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/x-ndjson")
	// Set explicitly so net/http does not transparently decompress for us.
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "response headers never arrived while the handler was still running: the response is buffered, not streamed")
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"), "compression should still be applied")

	type readResult struct {
		line string
		err  error
	}

	lines := make(chan readResult, 2)
	go func() {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			lines <- readResult{err: err}
			return
		}
		br := bufio.NewReader(zr)
		for {
			line, err := br.ReadString('\n')
			lines <- readResult{line: line, err: err}
			if err != nil {
				return
			}
		}
	}()

	select {
	case got := <-lines:
		require.NoError(t, got.err)
		require.Equal(t, firstRecord, got.line)
	case <-time.After(15 * time.Second):
		t.Fatal("first record never arrived while the handler was still running: the response is buffered, not streamed")
	}

	release()

	select {
	case got := <-lines:
		require.NoError(t, got.err)
		require.Equal(t, secondRecord, got.line)
	case <-time.After(10 * time.Second):
		t.Fatal("second record never arrived")
	}

	<-handlerDone
}

// The routing timeout has to leave the client room to receive the response.
// Helia's delegated routing client aborts the whole request at 30s and starts
// counting before someguy does, so anything at or above that loses every
// record someguy resolved.
func TestRoutingTimeoutLeavesRoomForClientDeadline(t *testing.T) {
	t.Parallel()

	const heliaClientTimeout = 30 * time.Second
	require.Less(t, DefaultRoutingTimeout, heliaClientTimeout)
	require.Less(t, DefaultRoutingTimeout, server.DefaultRoutingTimeout)
}
