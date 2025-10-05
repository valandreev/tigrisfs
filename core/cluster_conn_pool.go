// Copyright 2024 Tigris Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !windows

package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/valandreev/tigrisfs/core/cfg"
	"github.com/valandreev/tigrisfs/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const OUTSTAGE_TIMEOUT = 10 * time.Second

var connsLog = log.GetLogger("conns")

type Peer struct {
	mu      sync.RWMutex
	address string
	conn    *grpc.ClientConn
}

type ConnPool struct {
	flags *cfg.FlagStorage
	id    NodeId
	peers map[NodeId]*Peer
}

type Request func(ctx context.Context, conn *grpc.ClientConn) error

func NewConnPool(flags *cfg.FlagStorage) *ConnPool {
	id := NodeId(flags.ClusterMe.Id)

	peers := make(map[NodeId]*Peer)
	for _, node := range flags.ClusterPeers {
		peers[NodeId(node.Id)] = &Peer{
			address: node.Address,
		}
	}

	return &ConnPool{
		flags: flags,
		id:    id,
		peers: peers,
	}
}

func (conns *ConnPool) Unary(
	nodeId NodeId,
	makeRequst Request,
) (err error) {
	return conns.UnaryConfiguarble(nodeId, makeRequst, true)
}

func (conns *ConnPool) UnaryConfiguarble(
	nodeId NodeId,
	makeRequst Request,
	unmountOnError bool,
) (err error) {
	if unmountOnError {
		defer func() {
			if err != nil {
				go func() {
					connsLog.Infof("error on request to %v umount", nodeId)
					_ = TryUnmount(conns.flags.MountPoint)
				}()
			}
		}()
	}

	peer := conns.peers[nodeId]
	peer.mu.RLock()

	if peer.conn == nil {
		peer.mu.RUnlock()
		peer.mu.Lock()
		if peer.conn == nil {
			var conn *grpc.ClientConn
			conn, err = grpc.Dial(peer.address, //nolint:staticcheck
				grpc.WithInsecure(),                //nolint:staticcheck
				grpc.WithBlock(),                   //nolint:staticcheck
				grpc.WithTimeout(OUTSTAGE_TIMEOUT), //nolint:staticcheck
				grpc.WithChainUnaryInterceptor(
					LogClientInterceptor,
				),
			)
			if err != nil {
				peer.mu.Unlock()
				return
			}
			peer.conn = conn
		}
		peer.mu.Unlock()
		peer.mu.RLock()
	}

	ctx, cancel := conns.ctx(nodeId)
	defer cancel()
	err = makeRequst(ctx, peer.conn)

	peer.mu.RUnlock()

	if err != nil {
		peer.mu.Lock()
		peer.conn = nil
		peer.mu.Unlock()
	}

	return
}

func (conns *ConnPool) Broad(
	makeRequst Request,
) (errs map[NodeId]error) {
	return conns.BroadConfigurable(makeRequst, true)
}

func (conns *ConnPool) BroadConfigurable(
	makeRequst Request,
	unmountOnError bool,
) (errs map[NodeId]error) {
	errs = make(map[NodeId]error)
	mu := sync.Mutex{}
	wg := sync.WaitGroup{}
	for nodeId := range conns.peers {
		if nodeId != conns.id {
			wg.Add(1)
			go func(nodeId NodeId) {
				err := conns.UnaryConfiguarble(nodeId, makeRequst, unmountOnError)
				if err != nil {
					mu.Lock()
					errs[nodeId] = err
					mu.Unlock()
				}
				wg.Done()
			}(nodeId)
		}
	}
	wg.Wait()
	return
}

func (conns *ConnPool) ctx(dstNodeId NodeId) (context.Context, context.CancelFunc) {
	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(
		ctx,
		SRC_NODE_ID_METADATA_KEY, fmt.Sprint(conns.id),
		DST_NODE_ID_METADATA_KEY, fmt.Sprint(dstNodeId),
	)
	ctx, cancel := context.WithTimeout(ctx, OUTSTAGE_TIMEOUT)
	return ctx, cancel
}
