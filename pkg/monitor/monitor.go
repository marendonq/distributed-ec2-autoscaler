package monitor

import (
    "context"
    "fmt"
    "log"
    "net"
    "strconv"
    "sync"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    "github.com/marendonq/distributed-ec2-autoscaler/config"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
    pb "github.com/marendonq/distributed-ec2-autoscaler/api/proto/monitor"
)

type monitorSServer struct {
    pb.UnimplementedMonitorSServiceServer
    svc *service.MonitorService
}

func (s *monitorSServer) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
    if req.Meta == nil {
        req.Meta = make(map[string]string)
    }
    req.Meta["grpc_port"] = fmt.Sprintf("%d", req.GrpcPort)
    req.Meta["failures"] = "0"
    
    inst := &domain.Instance{
        ID:       req.InstanceId,
        Hostname: req.Hostname,
        IP:       req.LocalIp,
        Meta:     req.Meta,
    }
    
    // Register sets LastSeen automatically
    if err := s.svc.RegisterInstance(inst); err != nil {
        return &pb.RegisterResponse{Success: false, Message: err.Error()}, nil
    }
    log.Printf("MonitorS: Registered instance %s (IP: %s, Port: %d)", inst.ID, req.LocalIp, req.GrpcPort)
    return &pb.RegisterResponse{Success: true, Message: "Registered successfully"}, nil
}

func (s *monitorSServer) Deregister(ctx context.Context, req *pb.DeregisterRequest) (*pb.DeregisterResponse, error) {
    if err := s.svc.Deregister(req.InstanceId); err != nil {
        return &pb.DeregisterResponse{Success: false}, nil
    }
    log.Printf("MonitorS: Deregistered instance %s", req.InstanceId)
    return &pb.DeregisterResponse{Success: true}, nil
}

// StartGRPCServer starts a gRPC server listening on addr. It returns when Serve exits.
func StartGRPCServer(ctx context.Context, addr string, svc *service.MonitorService) error {
    lis, err := net.Listen("tcp", addr)
    if err != nil {
        return err
    }
    srv := grpc.NewServer()
    pb.RegisterMonitorSServiceServer(srv, &monitorSServer{svc: svc})
    
    go func() {
        <-ctx.Done()
        log.Println("shutting down MonitorS gRPC server")
        srv.GracefulStop()
    }()
    log.Printf("MonitorS gRPC server listening on %s", addr)
    return srv.Serve(lis)
}

// StartSchedulers launches background periodic tasks to poll MonitorC agents.
func StartSchedulers(ctx context.Context, cfg *config.Config, svc interface{ 
    ListInstances() ([]*domain.Instance, error)
    RegisterInstance(*domain.Instance) error
    MarkInactive(string) error 
}) {
    interval := time.Duration(cfg.HeartbeatCheckIntervalSeconds) * time.Second
    if interval <= 0 {
        interval = 10 * time.Second // Word doc suggests 10s
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
                
                var wg sync.WaitGroup
                for _, inst := range instances {
                    if inst.Status != domain.StatusActive {
                        continue
                    }
                    wg.Add(1)
                    go func(instance *domain.Instance) {
                        defer wg.Done()
                        pollInstance(instance, svc)
                    }(inst)
                }
                wg.Wait()
            }
        }
    }()
}

func pollInstance(inst *domain.Instance, svc interface{ RegisterInstance(*domain.Instance) error; MarkInactive(string) error }) {
    port := inst.Meta["grpc_port"]
    if port == "" {
        port = "50052" // default MonitorC port
    }
    target := fmt.Sprintf("%s:%s", inst.IP, port)
    
    // Configurar timeout corto para no bloquear (3s según Word doc)
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    
    conn, err := grpc.DialContext(ctx, target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
    
    success := false
    
    if err == nil {
        client := pb.NewMonitorCServiceClient(conn)
        // 1. Ping
        _, errPing := client.Ping(ctx, &pb.PingRequest{})
        if errPing == nil {
            success = true
            
            // 2. HU-03: Obtener métricas de CPU
            metricsResp, errMetrics := client.GetMetrics(ctx, &pb.GetMetricsRequest{})
            if errMetrics == nil && metricsResp.Success {
                inst.Meta["cpu_load"] = fmt.Sprintf("%.2f", metricsResp.CpuLoad)
            }
        }
        conn.Close()
    }

    // Manejo de fallos o éxitos
    failures, _ := strconv.Atoi(inst.Meta["failures"])
    
    if success {
        inst.LastSeen = time.Now().Unix()
        inst.Meta["failures"] = "0"
        svc.RegisterInstance(inst) // Actualizar datos
    } else {
        failures++
        inst.Meta["failures"] = fmt.Sprintf("%d", failures)
        if failures >= 3 {
            log.Printf("scheduler: marking %s inactive after 3 consecutive failures", inst.ID)
            svc.MarkInactive(inst.ID)
        } else {
            svc.RegisterInstance(inst) // Actualizar conteo de fallos
        }
    }
}
