package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	pbuf "github.com/gogo/protobuf/proto"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-delegated-routing/client"
	"github.com/ipfs/go-ipns"
	ipns_pb "github.com/ipfs/go-ipns/pb"
	"github.com/ipld/go-ipld-prime/codec/dagjson"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/multiformats/go-multiaddr"

	drp "github.com/ipfs/go-delegated-routing/gen/proto"
)

func identify(ctx context.Context, endpoint string, prettyOutput bool) error {
	ic, err := drp.New_DelegatedRouting_Client(endpoint)
	if err != nil {
		return err
	}

	respCh, err := ic.Identify_Async(ctx, &drp.DelegatedRouting_IdentifyArg{})
	for r := range respCh {
		if r.Err != nil {
			log.Println(r.Err)
			continue
		}

		if !prettyOutput {
			var buf bytes.Buffer
			if err := dagjson.Encode(r.Resp, &buf); err != nil {
				return err
			}
			fmt.Println(buf.String())
		} else {
			var methods []string
			for _, m := range r.Resp.Methods {
				methods = append(methods, string(m))
			}
			fmt.Println(strings.Join(methods, ","))
		}
	}
	return nil
}

func findprovs(ctx context.Context, c cid.Cid, endpoint string, prettyOutput bool) error {
	ic, err := drp.New_DelegatedRouting_Client(endpoint)
	if err != nil {
		return err
	}

	respCh, err := ic.FindProviders_Async(ctx, &drp.FindProvidersRequest{
		Key: drp.LinkToAny(c),
	})
	if err != nil {
		return err
	}
	for r := range respCh {
		if r.Err != nil {
			log.Println(r.Err)
			continue
		}

		if !prettyOutput {
			var buf bytes.Buffer
			if err := dagjson.Encode(r.Resp, &buf); err != nil {
				return err
			}
			fmt.Println(buf.String())
		} else {
			for _, prov := range r.Resp.Providers {
				if prov.ProviderNode.Peer != nil {
					ai := &peer.AddrInfo{}
					ai.ID = peer.ID(prov.ProviderNode.Peer.ID)
					for _, bma := range prov.ProviderNode.Peer.Multiaddresses {
						ma, err := multiaddr.NewMultiaddrBytes(bma)
						if err != nil {
							return err
						}
						ai.Addrs = append(ai.Addrs, ma)
					}
					fmt.Println(ai)
				}
				for _, proto := range prov.ProviderProto {
					if proto.Bitswap != nil {
						fmt.Println("\t Bitswap")
					} else if proto.GraphSyncFILv1 != nil {
						fmt.Println("\t GraphSyncFILv1")
						var buf bytes.Buffer
						if err := dagjson.Encode(proto.GraphSyncFILv1, &buf); err != nil {
							return err
						}
						fmt.Println("\t\t" + buf.String())
					} else {
						var buf bytes.Buffer
						if err := dagjson.Encode(proto, &buf); err != nil {
							return err
						}
						fmt.Println("\t" + buf.String())
					}
				}
			}
		}
	}
	return nil
}

func getIPNS(ctx context.Context, p peer.ID, endpoint string, prettyOutput bool) error {
	ic, err := drp.New_DelegatedRouting_Client(endpoint)
	if err != nil {
		return err
	}

	if prettyOutput {
		c := client.NewClient(ic)
		respCh, err := c.GetIPNSAsync(ctx, []byte(p))
		if err != nil {
			return err
		}
		for r := range respCh {
			if r.Err != nil {
				log.Println(r.Err)
				continue
			}
			rec  := new(ipns_pb.IpnsEntry)
			if err := pbuf.Unmarshal(r.Record, rec); err != nil {
				return err
			}
			seqno := rec.GetSequence()
			ttl := time.Duration(rec.GetTtl())
			eol, err := ipns.GetEOL(rec)
			if err != nil {
				return err
			}
			value := string(rec.GetValue())
			fmt.Printf("Sequence: %d, TTL: %v, EOL: %v, Value: %s\n", seqno, ttl, eol, value)
		}
		return nil
	}

	respCh, err := ic.GetIPNS_Async(ctx, &drp.GetIPNSRequest{
		ID: []byte(p),
	})
	if err != nil {
		return err
	}
	for r := range respCh {
		if r.Err != nil {
			log.Println(r.Err)
			continue
		}
		var buf bytes.Buffer
		if err := dagjson.Encode(r.Resp, &buf); err != nil {
			return err
		}
		fmt.Println(buf.String())
	}
	return nil
}

func putIPNS(ctx context.Context, key peer.ID, record []byte, endpoint string) error {
	ic, err := drp.New_DelegatedRouting_Client(endpoint)
	if err != nil {
		return err
	}

	c := client.NewClient(ic)
	return c.PutIPNS(ctx, []byte(key), record)
}
