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
    svc      *service.MonitorService
    eventSvc *service.EventService
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

    // HU-11: registrar evento cuando un MonitorC se conecta
    if s.eventSvc != nil {
        s.eventSvc.RecordEvent(domain.NewSystemEvent(
            domain.EventMonitorCRegistered,
            fmt.Sprintf("MonitorC %s registered from IP %s", req.InstanceId, req.LocalIp),
            map[string]string{"instance_id": req.InstanceId, "hostname": req.Hostname},
        ))
    }

    return &pb.RegisterResponse{Success: true, Message: "Registered successfully"}, nil
}

func (s *monitorSServer) Deregister(ctx context.Context, req *pb.DeregisterRequest) (*pb.DeregisterResponse, error) {
    if err := s.svc.Deregister(req.InstanceId); err != nil {
        return &pb.DeregisterResponse{Success: false}, nil
    }
    log.Printf("MonitorS: Deregistered instance %s", req.InstanceId)

    // HU-11: registrar evento cuando una instancia se desregistra
    if s.eventSvc != nil {
        s.eventSvc.RecordEvent(domain.NewSystemEvent(
            domain.EventInstanceDeleted,
            fmt.Sprintf("Instance %s deregistered", req.InstanceId),
            map[string]string{"instance_id": req.InstanceId},
        ))
    }

    return &pb.DeregisterResponse{Success: true}, nil
}

func (s *monitorSServer) GetAggregatedMetrics(ctx context.Context, req *pb.AggregatedMetricsRequest) (*pb.AggregatedMetricsResponse, error) {
    instances, err := s.svc.ListInstances()
    if err != nil {
        return &pb.AggregatedMetricsResponse{Success: false}, nil
    }

    var totalLoad float32 = 0
    activeCount := 0
    inactiveCount := 0
    
    for _, inst := range instances {
        if inst.Status == domain.StatusActive {
            activeCount++
            if loadStr, ok := inst.Meta["cpu_load"]; ok {
                if load, err := strconv.ParseFloat(loadStr, 32); err == nil {
                    totalLoad += float32(load)
                }
            }
        } else {
            inactiveCount++
        }
    }

    avgLoad := float32(0)
    if activeCount > 0 {
        avgLoad = totalLoad / float32(activeCount)
    }

    // Construir lista de instancias
    var instanceInfos []*pb.InstanceInfo
    for _, inst := range instances {
        load := float32(0)
        if loadStr, ok := inst.Meta["cpu_load"]; ok {
            if l, err := strconv.ParseFloat(loadStr, 32); err == nil {
                load = float32(l)
            }
        }
        instanceInfos = append(instanceInfos, &pb.InstanceInfo{
            Id:        inst.ID,
            Hostname:  inst.Hostname,
            Ip:        inst.IP,
            CpuLoad:   load,
            Status:    string(inst.Status),
            LastSeen:  inst.LastSeen,
        })
    }

    // HU-11: registrar metricas consolidadas como evento relevante
    if s.eventSvc != nil && activeCount > 0 {
        s.eventSvc.RecordEvent(domain.NewSystemEvent(
            domain.EventMetricsRecorded,
            fmt.Sprintf("Aggregated metrics: avg_cpu=%.1f%%, active=%d, inactive=%d, total=%d",
                avgLoad, activeCount, inactiveCount, len(instances)),
            map[string]string{
                "avg_cpu_load":   fmt.Sprintf("%.2f", avgLoad),
                "active_count":   fmt.Sprintf("%d", activeCount),
                "inactive_count": fmt.Sprintf("%d", inactiveCount),
                "total_count":    fmt.Sprintf("%d", len(instances)),
            },
        ))
    }

    return &pb.AggregatedMetricsResponse{
        Success:          true,
        AvgCpuLoad:       avgLoad,
        TotalInstances:   int32(len(instances)),
        ActiveInstances:  int32(activeCount),
        InactiveInstances: int32(inactiveCount),
        Instances:        instanceInfos,
    }, nil
}

func (s *monitorSServer) GetEvents(ctx context.Context, req *pb.GetEventsRequest) (*pb.GetEventsResponse, error) {
    filter := make(map[string]string)
    if req.EventType != nil && *req.EventType != "" {
        filter["type"] = *req.EventType
    }
    if req.AfterTimestamp != nil && *req.AfterTimestamp > 0 {
        filter["after_timestamp"] = fmt.Sprintf("%d", *req.AfterTimestamp)
    }

    events, err := s.svc.GetEvents(filter)
    if err != nil {
        return &pb.GetEventsResponse{Success: false}, nil
    }

    limit := 0
    if req.Limit != nil {
        limit = int(*req.Limit)
    }
    if limit > 0 && len(events) > limit {
        events = events[:limit]
    }

    pbEvents := make([]*pb.SystemEvent, 0, len(events))
    for _, e := range events {
        pbEvents = append(pbEvents, &pb.SystemEvent{
            Id:        e.ID,
            Type:      string(e.Type),
            Message:   e.Message,
            Metadata:  e.Metadata,
            Timestamp: e.Timestamp,
        })
    }

    return &pb.GetEventsResponse{Success: true, Events: pbEvents}, nil
}


// StartGRPCServer starts a gRPC server listening on addr. It returns when Serve exits.
func StartGRPCServer(ctx context.Context, addr string, svc *service.MonitorService, eventSvc *service.EventService) error {
    lis, err := net.Listen("tcp", addr)
    if err != nil {
        return err
    }
    srv := grpc.NewServer()
    pb.RegisterMonitorSServiceServer(srv, &monitorSServer{
        svc:      svc,
        eventSvc: eventSvc,
    })
    
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
}, eventSvc *service.EventService) {
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
                        pollInstance(instance, svc, eventSvc)
                    }(inst)
                }
                wg.Wait()
            }
        }
    }()
}

func pollInstance(inst *domain.Instance, svc interface{ RegisterInstance(*domain.Instance) error; MarkInactive(string) error }, eventSvc *service.EventService) {
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
        } else {
            err = errPing
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
        // HU-11: registrar fallo de conexion al hacer poll
        if eventSvc != nil {
            eventSvc.RecordEvent(domain.NewSystemEvent(
                domain.EventFailure,
                fmt.Sprintf("Poll failed for %s: %v", inst.ID, err),
                map[string]string{"instance_id": inst.ID, "host": inst.IP},
            ))
        }

        failures++
        inst.Meta["failures"] = fmt.Sprintf("%d", failures)
        if failures >= 3 {
            log.Printf("scheduler: marking %s inactive after 3 consecutive failures", inst.ID)
            svc.MarkInactive(inst.ID)
            // HU-11: registrar cuando el scheduler marca una instancia inactiva
            if eventSvc != nil {
                eventSvc.RecordEvent(domain.NewSystemEvent(
                    domain.EventInstanceMarkedInactive,
                    fmt.Sprintf("Instance %s marked inactive after consecutive failures", inst.ID),
                    map[string]string{"instance_id": inst.ID, "failures": inst.Meta["failures"]},
                ))
            }
        } else {
            svc.RegisterInstance(inst) // Actualizar conteo de fallos
        }
    }
}
