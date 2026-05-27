package monitor

import (
    "context"
    "log"
    "net"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/config"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "google.golang.org/grpc"
)

// StartGRPCServer starts a gRPC server listening on addr. It returns when Serve exits.
func StartGRPCServer(ctx context.Context, addr string) error {
    lis, err := net.Listen("tcp", addr)
    if err != nil {
        return err
    }
    srv := grpc.NewServer()
    go func() {
        <-ctx.Done()
        log.Println("shutting down gRPC server")
        srv.GracefulStop()
    }()
    log.Printf("gRPC server listening on %s", addr)
    return srv.Serve(lis)
}

// StartSchedulers launches background periodic tasks (heartbeats, cleanup, etc.).
// It uses the provided service to list instances and mark them inactive when
// they haven't been seen recently.
func StartSchedulers(ctx context.Context, cfg *config.Config, svc interface{ ListInstances() ([]*domain.Instance, error); MarkInactive(string) error }) {
    interval := time.Duration(cfg.HeartbeatCheckIntervalSeconds) * time.Second
    if interval <= 0 {
        interval = 30 * time.Second
    }
    ticker := time.NewTicker(interval)
    go func() {
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                log.Println("schedulers stopped")
                return
            case <-ticker.C:
                instances, err := svc.ListInstances()
                if err != nil {
                    log.Printf("scheduler: failed to list instances: %v", err)
                    continue
                }
                now := time.Now().Unix()
                timeout := int64(cfg.HeartbeatTimeoutSeconds)
                for _, inst := range instances {
                    if now-inst.LastSeen > timeout {
                        log.Printf("scheduler: marking %s inactive (last seen %d)", inst.ID, inst.LastSeen)
                        if err := svc.MarkInactive(inst.ID); err != nil {
                            log.Printf("scheduler: failed to mark inactive %s: %v", inst.ID, err)
                        }
                    }
                }
                log.Printf("scheduler tick: checked %d instances", len(instances))
            }
        }
    }()
}
