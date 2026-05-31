package main

import (
    "context"
    "log"
    "net/http"
    "os/signal"
    "syscall"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/config"
    "github.com/marendonq/distributed-ec2-autoscaler/pkg/monitor"
    persistence "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/persistence"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
    httpAdapter "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/http"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/controller"
)

func main() {
    cfg, err := config.LoadConfig("config.json")
    if err != nil {
        log.Fatalf("failed to load config: %v", err)
    }
    if err := config.ApplyEnvOverrides(cfg); err != nil {
        log.Fatalf("failed to apply env overrides: %v", err)
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

    // HU-11: inicializar event logger
    eventStore, err := persistence.NewSQLiteEventStore("teleproy2.db")
    if err != nil {
        log.Printf("[HU-11] sqlite event store unavailable, logging disabled: %v", err)
    }
    eventSvc := service.NewEventService(eventStore) // eventStore puede ser nil, hay nil-guard

    svc := service.NewMonitorService(reg, eventSvc)

    // HU-29: inicializar servidor HTTP para consulta de eventos
    go func() {
        if err := httpAdapter.StartHTTPServerWithEventService(ctx, svc, eventSvc, ":8080"); err != nil && err != http.ErrServerClosed {
            log.Printf("[HU-29] HTTP server error: %v", err)
        }
    }()

    // Start schedulers (pass service to allow marking inactive)
    monitor.StartSchedulers(ctx, cfg, svc, eventSvc)

    // Start gRPC server in background
    go func() {
        if err := monitor.StartGRPCServer(ctx, ":50051", svc, eventSvc); err != nil {
            log.Fatalf("gRPC server exited: %v", err)
        }
    }()

    // Start ControllerASG (HU-35 & HU-22)
    asg, err := controller.NewASGController(ctx, cfg, reg, eventSvc)
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
