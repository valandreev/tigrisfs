//go:build !windows

package core

import (
	"context"
	"github.com/yandex-cloud/geesefs/log"

	"github.com/yandex-cloud/geesefs/core/cfg"
	"github.com/yandex-cloud/geesefs/core/pb"
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
