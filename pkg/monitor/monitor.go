package monitor

import (
    "context"
    "log"
    "net"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/config"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
    pb "github.com/marendonq/distributed-ec2-autoscaler/api/proto/monitor"
    "google.golang.org/grpc"
)

type grpcServer struct {
    pb.UnimplementedMonitorServiceServer
    svc *service.MonitorService
}

func (s *grpcServer) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
    inst := &domain.Instance{
        ID:       req.InstanceId,
        Hostname: req.Hostname,
        IP:       req.LocalIp,
        Meta:     req.Meta,
    }
    if err := s.svc.RegisterInstance(inst); err != nil {
        return &pb.RegisterResponse{Success: false, Message: err.Error()}, nil
    }
    log.Printf("gRPC: Registered instance %s", inst.ID)
    return &pb.RegisterResponse{Success: true, Message: "Registered successfully"}, nil
}

func (s *grpcServer) Deregister(ctx context.Context, req *pb.DeregisterRequest) (*pb.DeregisterResponse, error) {
    if err := s.svc.Deregister(req.InstanceId); err != nil {
        return &pb.DeregisterResponse{Success: false}, nil
    }
    log.Printf("gRPC: Deregistered instance %s", req.InstanceId)
    return &pb.DeregisterResponse{Success: true}, nil
}

func (s *grpcServer) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
    if err := s.svc.Heartbeat(req.InstanceId); err != nil {
        return &pb.HeartbeatResponse{Success: false}, nil
    }
    // log.Printf("gRPC: Heartbeat from %s", req.InstanceId)
    return &pb.HeartbeatResponse{Success: true}, nil
}

func (s *grpcServer) GetMetrics(ctx context.Context, req *pb.GetMetricsRequest) (*pb.GetMetricsResponse, error) {
    // Para simplificar, el servidor no llama activamente al cliente. En un modelo push, 
    // el cliente enviaría las métricas. Aquí simplemente devolvemos un valor dummy, 
    // pero idealmente este método debería ser llamado por ControllerASG y enviaría la petición al cliente,
    // o el cliente reportaría sus métricas en el Heartbeat. 
    return &pb.GetMetricsResponse{CpuLoad: 50.0}, nil
}

// StartGRPCServer starts a gRPC server listening on addr. It returns when Serve exits.
func StartGRPCServer(ctx context.Context, addr string, svc *service.MonitorService) error {
    lis, err := net.Listen("tcp", addr)
    if err != nil {
        return err
    }
    srv := grpc.NewServer()
    pb.RegisterMonitorServiceServer(srv, &grpcServer{svc: svc})
    
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
                        if inst.Status == domain.StatusActive {
                            log.Printf("scheduler: marking %s inactive (last seen %d)", inst.ID, inst.LastSeen)
                            if err := svc.MarkInactive(inst.ID); err != nil {
                                log.Printf("scheduler: failed to mark inactive %s: %v", inst.ID, err)
                            }
                        }
                    }
                }
                // log.Printf("scheduler tick: checked %d instances", len(instances))
            }
        }
    }()
}
