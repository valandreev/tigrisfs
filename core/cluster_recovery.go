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

	"github.com/valandreev/tigrisfs/log"

	"github.com/valandreev/tigrisfs/core/cfg"
	"github.com/valandreev/tigrisfs/core/pb"
)

var recLog = log.GetLogger("rec")

type Recovery struct {
	pb.UnimplementedRecoveryServer
	Flags *cfg.FlagStorage
}

func (rec *Recovery) Unmount(ctx context.Context, req *pb.UnmountRequest) (*pb.UnmountResponse, error) {
	go func() {
		recLog.E(TryUnmount(rec.Flags.MountPoint))
	}()
	return &pb.UnmountResponse{}, nil
}
