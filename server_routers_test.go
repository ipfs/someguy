package main

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	mh "github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockRouter struct{ mock.Mock }

var _ router = &mockRouter{}

func (m *mockRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	args := m.Called(ctx, key, limit)
	if arg0 := args.Get(0); arg0 == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(iter.ResultIter[types.Record]), args.Error(1)
}

func (m *mockRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	args := m.Called(ctx, pid, limit)
	if arg0 := args.Get(0); arg0 == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(iter.ResultIter[*types.PeerRecord]), args.Error(1)
}

func (m *mockRouter) GetIPNS(ctx context.Context, name ipns.Name) (*ipns.Record, error) {
	args := m.Called(ctx, name)
	if arg0 := args.Get(0); arg0 == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ipns.Record), args.Error(1)
}

func (m *mockRouter) PutIPNS(ctx context.Context, name ipns.Name, record *ipns.Record) error {
	args := m.Called(ctx, name, record)
	return args.Error(0)
}

func makeName(t *testing.T) (crypto.PrivKey, ipns.Name) {
	sk, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)

	pid, err := peer.IDFromPrivateKey(sk)
	require.NoError(t, err)

	return sk, ipns.NameFromPeer(pid)
}

func makeIPNSRecord(t *testing.T, sk crypto.PrivKey, opts ...ipns.Option) (*ipns.Record, []byte) {
	cid, err := cid.Decode("bafkreifjjcie6lypi6ny7amxnfftagclbuxndqonfipmb64f2km2devei4")
	require.NoError(t, err)

	path := path.FromCid(cid)
	eol := time.Now().Add(time.Hour * 48)
	ttl := time.Second * 20

	record, err := ipns.NewRecord(sk, path, 1, eol, ttl, opts...)
	require.NoError(t, err)

	rawRecord, err := ipns.MarshalRecord(record)
	require.NoError(t, err)

	return record, rawRecord
}

func TestGetIPNS(t *testing.T) {
	t.Parallel()

	sk, name := makeName(t)
	rec, _ := makeIPNSRecord(t, sk)

	t.Run("OK (Multiple Composable, One Fails, One OK)", func(t *testing.T) {
		ctx := context.Background()

		mr1 := &mockRouter{}
		mr1.On("GetIPNS", mock.Anything, name).Return(rec, nil)

		mr2 := &mockRouter{}
		mr2.On("GetIPNS", mock.Anything, name).Return(nil, routing.ErrNotFound)

		r := parallelRouter{
			routers: []router{
				composableRouter{
					ipns: mr1,
				},
				composableRouter{
					ipns: mr2,
				},
			},
		}

		getRec, err := r.GetIPNS(ctx, name)
		require.NoError(t, err)
		require.EqualValues(t, rec, getRec)
	})

	t.Run("OK (Multiple Parallel)", func(t *testing.T) {
		ctx := context.Background()

		mr1 := &mockRouter{}
		mr1.On("GetIPNS", mock.Anything, name).Return(nil, routing.ErrNotFound)

		mr2 := &mockRouter{}
		mr2.On("GetIPNS", mock.Anything, name).Return(rec, nil)

		r := parallelRouter{
			routers: []router{
				composableRouter{
					ipns: parallelRouter{
						routers: []router{mr1, mr2},
					},
				},
			},
		}

		getRec, err := r.GetIPNS(ctx, name)
		require.NoError(t, err)
		require.EqualValues(t, rec, getRec)
	})

	t.Run("No Routers", func(t *testing.T) {
		ctx := context.Background()

		r := parallelRouter{
			routers: []router{
				composableRouter{
					ipns: parallelRouter{},
				},
			},
		}

		_, err := r.GetIPNS(ctx, name)
		require.ErrorIs(t, err, routing.ErrNotFound)
	})
}

func TestPutIPNS(t *testing.T) {
	t.Parallel()

	sk, name := makeName(t)
	rec, _ := makeIPNSRecord(t, sk)

	t.Run("OK (Multiple Composable)", func(t *testing.T) {
		ctx := context.Background()

		mr1 := &mockRouter{}
		mr1.On("PutIPNS", mock.Anything, name, rec).Return(nil)

		mr2 := &mockRouter{}
		mr2.On("PutIPNS", mock.Anything, name, rec).Return(nil)

		r := parallelRouter{
			routers: []router{
				composableRouter{
					ipns: mr1,
				},
				composableRouter{
					ipns: mr2,
				},
			},
		}

		err := r.PutIPNS(ctx, name, rec)
		require.NoError(t, err)

		mr1.AssertExpectations(t)
		mr2.AssertExpectations(t)
	})

	t.Run("OK (Multiple Parallel)", func(t *testing.T) {
		ctx := context.Background()

		mr1 := &mockRouter{}
		mr1.On("PutIPNS", mock.Anything, name, rec).Return(nil)

		mr2 := &mockRouter{}
		mr2.On("PutIPNS", mock.Anything, name, rec).Return(nil)

		r := parallelRouter{
			routers: []router{
				composableRouter{
					ipns: parallelRouter{
						routers: []router{mr1, mr2},
					},
				},
			},
		}

		err := r.PutIPNS(ctx, name, rec)
		require.NoError(t, err)

		mr1.AssertExpectations(t)
		mr2.AssertExpectations(t)
	})

	t.Run("Failure of a Single Router (Multiple Composable)", func(t *testing.T) {
		ctx := context.Background()

		mr1 := &mockRouter{}
		mr1.On("PutIPNS", mock.Anything, name, rec).Return(errors.New("failed"))

		mr2 := &mockRouter{}
		mr2.On("PutIPNS", mock.Anything, name, rec).Return(nil)

		r := parallelRouter{
			routers: []router{
				composableRouter{
					ipns: mr1,
				},
				composableRouter{
					ipns: mr2,
				},
			},
		}

		err := r.PutIPNS(ctx, name, rec)
		require.ErrorContains(t, err, "failed")

		mr1.AssertExpectations(t)
		mr2.AssertExpectations(t)
	})

	t.Run("Failure of a Single Router (Multiple Parallel)", func(t *testing.T) {
		ctx := context.Background()

		mr1 := &mockRouter{}
		mr1.On("PutIPNS", mock.Anything, name, mock.Anything).Return(errors.New("failed"))

		mr2 := &mockRouter{}
		mr2.On("PutIPNS", mock.Anything, name, mock.Anything).Return(nil)

		r := parallelRouter{
			routers: []router{
				composableRouter{
					ipns: parallelRouter{
						routers: []router{mr1, mr2},
					},
				},
			},
		}

		err := r.PutIPNS(ctx, name, rec)
		require.ErrorContains(t, err, "failed")

		mr1.AssertExpectations(t)
		mr2.AssertExpectations(t)
	})
}

func TestFindProviders(t *testing.T) {
	t.Parallel()

	t.Run("Basic", func(t *testing.T) {
		prefix := cid.NewPrefixV1(cid.Raw, mh.SHA2_256)
		c, _ := prefix.Sum([]byte("foo"))

		ctx := context.Background()

		d := parallelRouter{}
		it, err := d.FindProviders(ctx, c, 10)

		require.NoError(t, err)
		require.False(t, it.Next())

		mr1 := &mockRouter{}
		mr1.On("FindProviders", mock.Anything, c, 10).Return(iter.ToResultIter(iter.FromSlice([]types.Record{})), nil)

		d = parallelRouter{
			routers: []router{
				&composableRouter{
					providers: mr1,
				},
			},
		}

		it, err = d.FindProviders(ctx, c, 10)
		require.NoError(t, err)
		require.False(t, it.Next())
	})
}
