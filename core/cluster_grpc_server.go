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
	"net"

	"github.com/valandreev/tigrisfs/core/cfg"
	"github.com/valandreev/tigrisfs/core/pb"
	"github.com/valandreev/tigrisfs/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
)

var grpcLog = log.GetLogger("grpc")

type GrpcServer struct {
	*grpc.Server
	flags *cfg.FlagStorage
}

func NewGrpcServer(flags *cfg.FlagStorage) *GrpcServer {
	return &GrpcServer{
		Server: grpc.NewServer(grpc.ChainUnaryInterceptor(
			LogServerInterceptor,
		)),
		flags: flags,
	}
}

func (srv *GrpcServer) Start() error {
	grpcLog.Infof("start server")
	lis, err := net.Listen("tcp", srv.flags.ClusterMe.Address)
	if err != nil {
		return err
	}
	if srv.flags.ClusterGrpcReflection {
		grpcLog.Infof("enable grpc reflection")
		reflection.Register(srv)
	}
	if err = srv.Serve(lis); err != nil {
		return err
	}
	return nil
}

const (
	SRC_NODE_ID_METADATA_KEY = "src-node-id"
	DST_NODE_ID_METADATA_KEY = "dst-node-id"
)

var traceLog = log.GetLogger("trace")

func LogServerInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	// theese requests generate a lot of bytes of logs, so disable it for now
	_, ok1 := req.(*pb.WriteFileRequest)
	_, ok2 := req.(*pb.ReadFileRequest)
	_, ok3 := req.(*pb.ReadDirRequest)
	ok := ok1 || ok2 || ok3

	src := "<unknown>"
	dst := "<unknown>"
	md, okMd := metadata.FromIncomingContext(ctx)
	if okMd {
		if keys, okKeys := md[SRC_NODE_ID_METADATA_KEY]; okKeys {
			if len(keys) >= 1 {
				src = keys[0]
			}
		}
		if keys, okKeys := md[DST_NODE_ID_METADATA_KEY]; okKeys {
			if len(keys) >= 1 {
				dst = keys[0]
			}
		}
	}

	if !ok {
		traceLog.Debug().Msgf("%s --> %s : %s : %+v", src, dst, info.FullMethod, req)
	}
	resp, err = handler(ctx, req)
	if !ok {
		traceLog.Debug().Msgf("%s <-- %s : %s : %+v", src, dst, info.FullMethod, req)
	}
	return
}

func LogClientInterceptor(ctx context.Context, method string, req, resp interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	// theese requests generate a lot of bytes of logs, so disable it for now
	_, ok1 := req.(*pb.WriteFileRequest)
	_, ok2 := req.(*pb.ReadFileRequest)
	_, ok3 := req.(*pb.ReadDirRequest)
	ok := ok1 || ok2 || ok3

	src := "<unknown>"
	dst := "<unknown>"
	md, okMd := metadata.FromOutgoingContext(ctx)
	if okMd {
		if keys, okKeys := md[SRC_NODE_ID_METADATA_KEY]; okKeys {
			if len(keys) >= 1 {
				src = keys[0]
			}
		}
		if keys, okKeys := md[DST_NODE_ID_METADATA_KEY]; okKeys {
			if len(keys) >= 1 {
				dst = keys[0]
			}
		}
	}

	if !ok {
		traceLog.Debug().Msgf("%s --> %s : %s : %+v", src, dst, method, req)
	}
	err := invoker(ctx, method, req, resp, cc, opts...)
	if !ok {
		traceLog.Debug().Msgf("%s <-- %s : %s : %+v", src, dst, method, req)
	}
	return err
}
