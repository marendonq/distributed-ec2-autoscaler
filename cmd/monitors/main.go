package main

import (
    "context"
    "log"
    "os/signal"
    "syscall"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/config"
    "github.com/marendonq/distributed-ec2-autoscaler/pkg/monitor"
    persistence "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/persistence"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/controller"
)

func main() {
    cfg, err := config.LoadConfig("config.json")
    if err != nil {
        log.Fatalf("failed to load config: %v", err)
    }
    // Ensure heartbeat defaults if config file contains zero values
    if cfg.HeartbeatCheckIntervalSeconds <= 0 {
        cfg.HeartbeatCheckIntervalSeconds = 30
    }
    if cfg.HeartbeatTimeoutSeconds <= 0 {
        cfg.HeartbeatTimeoutSeconds = 90
    }
    log.Printf("MonitorS starting with config: %+v", cfg)

    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    // create registry and service (hexagonal adapters)
    // Use SQLite as required by the Word doc
    var reg ports.InstanceRegistry
    sq, err := persistence.NewSQLiteRegistry("teleproy2.db")
    if err != nil {
        log.Printf("failed to open sqlite registry, falling back to in-memory: %v", err)
        reg = persistence.NewInMemoryRegistry()
    } else {
        reg = sq
    }
    svc := service.NewMonitorService(reg)

    // Start schedulers (pass service to allow marking inactive)
    monitor.StartSchedulers(ctx, cfg, svc)

    // Start gRPC server in background
    go func() {
        if err := monitor.StartGRPCServer(ctx, ":50051", svc); err != nil {
            log.Fatalf("gRPC server exited: %v", err)
        }
    }()

    // Start ControllerASG (HU-35 & HU-22)
    asg, err := controller.NewASGController(ctx, cfg, reg)
    if err != nil {
        log.Fatalf("failed to initialize ControllerASG: %v", err)
    }
    go asg.Start(ctx)

    // Wait for shutdown signal
    <-ctx.Done()
    // give background tasks a moment to exit
    time.Sleep(500 * time.Millisecond)
    log.Println("MonitorS shut down")
}
