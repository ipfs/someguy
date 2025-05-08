package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPBlockRouter(t *testing.T) {
	t.Parallel()
	debug := os.Getenv("DEBUG") == "true"

	t.Run("FindProviders", func(t *testing.T) {
		ctx := context.Background()
		// Set up mock HTTP Provider (trustless gateway) that returns HTTP 200 for specific CID
		testData := "Thu  8 May 01:07:03 CEST 2025"
		testCid := cid.MustParse("bafkreie5zycmytdhd5bl4f5jqsayyiwshugf57d4hkd7eif3toh23fsy3i")
		httpBlockGateway := newMockTrustlessGateway(testCid, testData, debug)
		t.Cleanup(func() { httpBlockGateway.Close() })

		// Test args
		endpoint := httpBlockGateway.URL
		peerId, _ := peer.Decode("12D3KooWCjfPiojcCUmv78Wd1NJzi4Mraj1moxigp7AfQVQvGLwH")
		insecureSkipVerify := true
		client := defaultHTTPBlockRouterClient(insecureSkipVerify)
		httpHost, httpPort, err := splitHostPort(endpoint)
		assert.NoError(t, err)
		expectedAddr := fmt.Sprintf("/ip4/%s/tcp/%s/tls/http", httpHost, httpPort)

		// Create Router
		httpBlockRouter, err := newHTTPBlockRouter(endpoint, peerId, client)
		assert.NoError(t, err)

		t.Run("return gateway as HTTP provider if HTTP HEAD check returned HTTP 200", func(t *testing.T) {
			t.Parallel()

			// Ask Router for CID present on trustless gateway
			it, err := httpBlockRouter.FindProviders(ctx, testCid, 10)
			require.NoError(t, err)

			results, err := iter.ReadAllResults(it)
			require.NoError(t, err)
			require.Len(t, results, 1)

			// Verify returned provider points at http gateway URL
			peerRecord := results[0].(*types.PeerRecord)
			require.Equal(t, peerId, *peerRecord.ID)
			require.Len(t, peerRecord.Addrs, 1)
			assert.NoError(t, err)
			require.Equal(t, expectedAddr, peerRecord.Addrs[0].String())
		})

		t.Run("return no results if HTTP HEAD check returned HTTP 404", func(t *testing.T) {
			t.Parallel()

			// This CID has no providers
			failCid := cid.MustParse("bafkreie5keu4z5kgutjds5tz3ahdxhcdkn4hl2vr7snenml44ui7y4yfki")

			// Ask Router for CID present on trustless gateway
			it, err := httpBlockRouter.FindProviders(ctx, failCid, 10)
			require.NoError(t, err)

			results, err := iter.ReadAllResults(it)
			require.NoError(t, err)
			require.Len(t, results, 0)
		})

	})
}

// newMockTrustlessGateway pretends to be http provider that supports
// block response https://specs.ipfs.tech/http-gateways/trustless-gateway/#block-responses-application-vnd-ipld-raw
func newMockTrustlessGateway(c cid.Cid, body string, debug bool) *httptest.Server {
	expectedPathPrefix := "/ipfs/" + c.String()
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if debug {
			fmt.Printf("mockTrustlessGateway %s %s\n", req.Method, req.URL.Path)
		}
		if strings.HasPrefix(req.URL.Path, expectedPathPrefix) {
			w.Header().Set("Content-Type", "application/vnd.ipld.raw")
			w.WriteHeader(http.StatusOK)
			if req.Method == "GET" {
				_, err := w.Write([]byte(body))
				if err != nil {
					fmt.Fprintf(os.Stderr, "mockTrustlessGateway %s %s error: %v\n", req.Method, req.URL.Path, err)
				}
			}
			return
		} else if strings.HasPrefix(req.URL.Path, "/ipfs/bafkqaaa") {
			// This is probe from https://specs.ipfs.tech/http-gateways/trustless-gateway/#dedicated-probe-paths
			w.Header().Set("Content-Type", "application/vnd.ipld.raw")
			w.WriteHeader(http.StatusOK)
			return
		} else {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
	})

	// Make it HTTP/2 with self-signed TLS cert
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	return srv
}

func splitHostPort(httpUrl string) (ipAddr string, port string, err error) {
	u, err := url.Parse(httpUrl)
	if err != nil {
		return "", "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("invalid URL format: missing scheme or host")
	}
	ipAddr, port, err = net.SplitHostPort(u.Host)
	if err != nil {
		return "", "", fmt.Errorf("failed to split host and port from %q: %w", u.Host, err)
	}
	return ipAddr, port, nil
}
