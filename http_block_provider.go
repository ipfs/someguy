package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	drclient "github.com/ipfs/boxo/routing/http/client"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const httpBlockProviderTimeout = 5 * time.Second

type httpBlockProvider struct {
	endpoint   string
	endpointMa multiaddr.Multiaddr
	peerID     peer.ID
	httpClient *http.Client
}

func newHTTPBlockProvider(endpoint string, p peer.ID, client *http.Client) (httpBlockProvider, error) {
	if client == nil {
		client = defaultHTTPBlockProviderClient()
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return httpBlockProvider{}, fmt.Errorf("failed to parse endpoint %s: %w", endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return httpBlockProvider{}, fmt.Errorf("unsupported scheme %s, only http and https are supported", u.Scheme)
	}

	h := u.Hostname()
	ip := net.ParseIP(h)
	var hostComponent string
	if ip == nil {
		hostComponent = "dns"
	} else if strings.Contains(h, ":") {
		hostComponent = "ip6"
	} else {
		hostComponent = "ip4"
	}

	var port int
	if u.Port() != "" {
		if p, err := strconv.Atoi(u.Port()); err != nil {
			return httpBlockProvider{}, fmt.Errorf("invalid port %s: %w", u.Port(), err)
		} else {
			port = p
		}
	} else {
		if u.Scheme == "https" {
			port = 443
		} else {
			port = 80
		}
	}

	var tlsComponent string
	if u.Scheme == "https" {
		tlsComponent = "/tls"
	} else if os.Getenv("DEBUG") == "true" {
		// allow unencrypted HTTP for local debugging
		tlsComponent = ""
	} else {
		return httpBlockProvider{}, fmt.Errorf("failed to parse endpoint %s: only HTTPS providers are allowed (unencrypted HTTP can't be used in web browsers)", endpoint)

	}

	var httpPathComponent string
	if escPath := u.EscapedPath(); escPath != "" && escPath != "/" {
		return httpBlockProvider{}, fmt.Errorf("failed to parse endpoint %s: only URLs without path are supported", endpoint)
	}

	endpointMaStr := fmt.Sprintf("/%s/%s/tcp/%d%s/http%s", hostComponent, h, port, tlsComponent, httpPathComponent)

	ma, err := multiaddr.NewMultiaddr(endpointMaStr)
	if err != nil {
		return httpBlockProvider{}, fmt.Errorf("failed to parse endpoint %s: %w", endpoint, err)
	}
	return httpBlockProvider{
		endpoint:   endpoint,
		endpointMa: ma,
		peerID:     p,
		httpClient: client,
	}, nil
}

func defaultHTTPBlockProviderClient() *http.Client {
	return &http.Client{
		Timeout: httpBlockProviderTimeout, // timeout hanging HTTP HEAD sooner than boxo/routing/http/server.DefaultRoutingTimeout
		Transport: &drclient.ResponseBodyLimitedTransport{
			RoundTripper: http.DefaultTransport,
			LimitBytes:   1 << 12, // max 4KiB -- should be plenty for HEAD response
			UserAgent:    "someguy/" + buildVersion(),
		},
	}
}

func (h httpBlockProvider) FindProviders(ctx context.Context, c cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", fmt.Sprintf("%s/ipfs/%s?format=raw", h.endpoint, c), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.ipld.raw")
	httpClient := h.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		return iter.ToResultIter(iter.FromSlice([]types.Record{
			&types.PeerRecord{
				Schema: types.SchemaPeer,
				ID:     &h.peerID,
				Addrs: []types.Multiaddr{
					{Multiaddr: h.endpointMa},
				},
				Protocols: []string{"transport-ipfs-gateway-http"},
				Extra:     nil,
			},
		})), nil
	}
	// everything that is not HTTP 200, including errors, produces empty response
	return iter.ToResultIter(iter.FromSlice([]types.Record{})), nil
}

var _ providersRouter = (*httpBlockProvider)(nil)
