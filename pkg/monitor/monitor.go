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
    "google.golang.org/grpc/keepalive"

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
        // HU-29: clasificar severidad del evento
        s.eventSvc.RecordEvent(domain.NewSystemEvent(
            domain.EventMonitorCRegistered,
            domain.SeverityInfo,
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
        // HU-29: clasificar severidad del evento
        s.eventSvc.RecordEvent(domain.NewSystemEvent(
            domain.EventInstanceDeleted,
            domain.SeverityInfo,
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
    contributing := 0

    for _, inst := range instances {
        if inst.Status != domain.StatusActive {
            inactiveCount++
            continue
        }
        activeCount++
        if inst.ExcludedFromAvg() {
            continue
        }
        if loadStr, ok := inst.Meta["cpu_load"]; ok {
            if load, err := strconv.ParseFloat(loadStr, 32); err == nil {
                totalLoad += float32(load)
                contributing++
            }
        }
    }

    avgLoad := float32(0)
    if contributing > 0 {
        avgLoad = totalLoad / float32(contributing)
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
        // HU-29: clasificar severidad del evento
        s.eventSvc.RecordEvent(domain.NewSystemEvent(
            domain.EventMetricsRecorded,
            domain.SeverityInfo,
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
    if req.EventType != "" {
        filter["type"] = req.EventType
    }
    // HU-29: soportar filtro por severity en gRPC
    if req.Severity != "" {
        filter["severity"] = req.Severity
    }
    if req.AfterTimestamp > 0 {
        filter["after_timestamp"] = fmt.Sprintf("%d", req.AfterTimestamp)
    }

    events, err := s.svc.GetEvents(filter)
    if err != nil {
        return &pb.GetEventsResponse{Success: false}, nil
    }

    limit := 0
    if req.Limit > 0 {
        limit = int(req.Limit)
    }
    if limit > 0 && len(events) > limit {
        events = events[:limit]
    }

    pbEvents := make([]*pb.SystemEvent, 0, len(events))
    for _, e := range events {
        pbEvents = append(pbEvents, &pb.SystemEvent{
            Id:        e.ID,
            Type:      string(e.Type),
            Severity:  string(e.Severity),
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
    srv := grpc.NewServer(
        grpc.KeepaliveParams(keepalive.ServerParameters{
            Time:    30 * time.Second,
            Timeout: 5 * time.Second,
        }),
    )
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

// DeadInstanceHandler is invoked after consecutive poll failures declare an instance dead.
type DeadInstanceHandler func(ctx context.Context, inst *domain.Instance) error

// SchedulerOpts configures MonitorS polling behavior.
type SchedulerOpts struct {
	GRPCTimeout      time.Duration
	MaxFailures      int
	OnDeadInstance   DeadInstanceHandler
}

// StartSchedulers launches background periodic tasks to poll MonitorC agents.
func StartSchedulers(ctx context.Context, cfg *config.Config, svc interface {
	ListInstances() ([]*domain.Instance, error)
	RegisterInstance(*domain.Instance) error
	RecordMetric(instanceID string, load float32, timestamp int64) error
}, eventSvc *service.EventService, opts SchedulerOpts) {
	interval := time.Duration(cfg.HeartbeatCheckIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if opts.GRPCTimeout <= 0 {
		opts.GRPCTimeout = time.Duration(cfg.GRPCTimeoutSeconds) * time.Second
	}
	if opts.GRPCTimeout <= 0 {
		opts.GRPCTimeout = 3 * time.Second
	}
	if opts.MaxFailures <= 0 {
		opts.MaxFailures = 3
	}
	defaultPort := cfg.MonitorCPort
	if defaultPort <= 0 {
		defaultPort = 50052
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
						pollInstance(ctx, instance, svc, eventSvc, opts, defaultPort)
					}(inst)
				}
				wg.Wait()
			}
		}
	}()
}

func pollInstance(ctx context.Context, inst *domain.Instance, svc interface {
	RegisterInstance(*domain.Instance) error
	RecordMetric(instanceID string, load float32, timestamp int64) error
}, eventSvc *service.EventService, opts SchedulerOpts, defaultPort int) {
	if inst.Meta == nil {
		inst.Meta = make(map[string]string)
	}
	port := inst.Meta["grpc_port"]
	if port == "" {
		port = fmt.Sprintf("%d", defaultPort)
	}
	target := fmt.Sprintf("%s:%s", inst.IP, port)

	pollCtx, cancel := context.WithTimeout(ctx, opts.GRPCTimeout)
	defer cancel()

	conn, err := grpc.DialContext(pollCtx, target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())

	success := false
	var pollErr error

	if err == nil {
		client := pb.NewMonitorCServiceClient(conn)
		_, errPing := client.Ping(pollCtx, &pb.PingRequest{})
		if errPing == nil {
			metricsResp, errMetrics := client.GetMetrics(pollCtx, &pb.GetMetricsRequest{})
			if errMetrics == nil && metricsResp.Success {
				if metricsResp.InstanceId != "" && metricsResp.InstanceId != inst.ID {
					pollErr = fmt.Errorf("instance_id mismatch: expected %s, got %s", inst.ID, metricsResp.InstanceId)
				} else {
					success = true
					inst.Meta["cpu_load"] = fmt.Sprintf("%.2f", metricsResp.CpuLoad)
					_ = svc.RecordMetric(inst.ID, metricsResp.CpuLoad, metricsResp.Timestamp)
				}
			} else {
				pollErr = errMetrics
			}
		} else {
			pollErr = errPing
		}
		conn.Close()
	} else {
		pollErr = err
	}

	failures, _ := strconv.Atoi(inst.Meta["failures"])

	if success {
		inst.LastSeen = time.Now().Unix()
		inst.Meta["failures"] = "0"
		delete(inst.Meta, "excluded_from_avg")
		_ = svc.RegisterInstance(inst)
		return
	}

	if eventSvc != nil {
		eventSvc.RecordEvent(domain.NewSystemEvent(
			domain.EventFailure,
			domain.SeverityCritical,
			fmt.Sprintf("Poll failed for %s: %v", inst.ID, pollErr),
			map[string]string{"instance_id": inst.ID, "host": inst.IP},
		))
	}

	failures++
	inst.Meta["failures"] = fmt.Sprintf("%d", failures)
	inst.Meta["excluded_from_avg"] = "true"

	if failures >= opts.MaxFailures {
		log.Printf("scheduler: instance %s declared dead after %d consecutive failures", inst.ID, failures)
		if eventSvc != nil {
			eventSvc.RecordEvent(domain.NewSystemEvent(
				domain.EventInstanceMarkedInactive,
				domain.SeverityWarning,
				fmt.Sprintf("Instance %s declared dead after consecutive failures", inst.ID),
				map[string]string{"instance_id": inst.ID, "failures": inst.Meta["failures"]},
			))
		}
		if opts.OnDeadInstance != nil {
			dead := *inst
			if err := opts.OnDeadInstance(ctx, &dead); err != nil {
				log.Printf("scheduler: dead instance handler failed for %s: %v", inst.ID, err)
			}
		}
		return
	}

	_ = svc.RegisterInstance(inst)
}
