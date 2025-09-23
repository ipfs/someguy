package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/routing/http/client"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
)

func findProviders(ctx context.Context, key cid.Cid, endpoint string, prettyOutput bool) error {
	drc, err := client.New(endpoint, client.WithDisabledLocalFiltering(true))
	if err != nil {
		return err
	}

	recordsIter, err := drc.FindProviders(ctx, key)
	if err != nil {
		return err
	}
	defer recordsIter.Close()

	for recordsIter.Next() {
		res := recordsIter.Val()

		// Check for error, but do not complain if we exceeded the timeout. We are
		// expecting that to happen: we explicitly defined a timeout.
		if res.Err != nil {
			if !errors.Is(res.Err, context.DeadlineExceeded) {
				return res.Err
			}

			return nil
		}

		if prettyOutput {
			switch res.Val.GetSchema() {
			case types.SchemaPeer:
				record := res.Val.(*types.PeerRecord)
				fmt.Fprintln(os.Stdout, record.ID)
				fmt.Fprintln(os.Stdout, "\tProtocols:", record.Protocols)
				fmt.Fprintln(os.Stdout, "\tAddresses:", record.Addrs)

				//lint:ignore SA1019 // ignore staticcheck
			case types.SchemaBitswap:
				//lint:ignore SA1019 // ignore staticcheck
				record := res.Val.(*types.BitswapRecord)
				fmt.Fprintln(os.Stdout, record.ID)
				fmt.Fprintln(os.Stdout, "\tProtocol:", record.Protocol)
				fmt.Fprintln(os.Stdout, "\tAddresses:", record.Addrs)

			default:
				// This is an unknown schema. Let's just print it raw.
				err := json.NewEncoder(os.Stdout).Encode(res.Val)
				if err != nil {
					return err
				}
			}

			fmt.Fprintln(os.Stdout)
		} else {
			err := json.NewEncoder(os.Stdout).Encode(res.Val)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func findPeers(ctx context.Context, pid peer.ID, endpoint string, prettyOutput bool) error {
	drc, err := client.New(endpoint, client.WithDisabledLocalFiltering(true))
	if err != nil {
		return err
	}

	recordsIter, err := drc.FindPeers(ctx, pid)
	if err != nil {
		return err
	}
	defer recordsIter.Close()

	for recordsIter.Next() {
		res := recordsIter.Val()

		// Check for error, but do not complain if we exceeded the timeout. We are
		// expecting that to happen: we explicitly defined a timeout.
		if res.Err != nil {
			if !errors.Is(res.Err, context.DeadlineExceeded) {
				return res.Err
			}

			return nil
		}

		if prettyOutput {
			fmt.Fprintln(os.Stdout, res.Val.ID)
			fmt.Fprintln(os.Stdout, "\tProtocols:", res.Val.Protocols)
			fmt.Fprintln(os.Stdout, "\tAddresses:", res.Val.Addrs)
			fmt.Fprintln(os.Stdout)
		} else {
			err := json.NewEncoder(os.Stdout).Encode(res.Val)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func getIPNS(ctx context.Context, name ipns.Name, endpoint string, prettyOutput bool) error {
	drc, err := client.New(endpoint)
	if err != nil {
		return err
	}

	rec, err := drc.GetIPNS(ctx, name)
	if err != nil {
		return err
	}

	if prettyOutput {
		v, err := rec.Value()
		if err != nil {
			return err
		}

		seq, err := rec.Sequence()
		if err != nil {
			return err
		}

		eol, err := rec.Validity()
		if err != nil {
			return err
		}

		fmt.Printf("/ipns/%s\n", name)

		// Since [client.Client.GetIPNS] verifies if the retrieved record is valid, we
		// do not need to verify it again. However, if you were not using this specific
		// client, but using some other tool, you should always validate the IPNS Record
		// using the [ipns.Validate] or [ipns.ValidateWithName] functions.
		fmt.Println("\tSignature Validated")
		fmt.Println("\tValue:", v.String())
		fmt.Println("\tSequence:", seq)
		fmt.Println("\tValidityType : EOL/End-of-Life")
		fmt.Println("\tValidity:", eol.Format(time.RFC3339))
		if ttl, err := rec.TTL(); err == nil {
			fmt.Println("\tTTL:", ttl.String())
		}

		return nil
	}

	raw, err := ipns.MarshalRecord(rec)
	if err != nil {
		return err
	}

	_, err = os.Stdout.Write(raw)
	return err
}

func putIPNS(ctx context.Context, name ipns.Name, record []byte, endpoint string) error {
	drc, err := client.New(endpoint)
	if err != nil {
		return err
	}

	rec, err := ipns.UnmarshalRecord(record)
	if err != nil {
		return err
	}

	return drc.PutIPNS(ctx, name, rec)
}
