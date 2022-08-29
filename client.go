package main

import (
	"bytes"
	"context"
	"fmt"
	"log"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/codec/dagjson"

	drp "github.com/ipfs/go-delegated-routing/gen/proto"
)

func ask(ctx context.Context, c cid.Cid, endpoint string) error {
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
		var buf bytes.Buffer
		if r.Err != nil {
			log.Println(r.Err)
			continue
		}
		if err := dagjson.Encode(r.Resp, &buf); err != nil {
			return err
		}
		fmt.Println(buf.String())
	}
	return nil
}
