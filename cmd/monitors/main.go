package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/config"
    "github.com/marendonq/distributed-ec2-autoscaler/pkg/monitor"
    persistence "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/persistence"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
    httpadapter "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/http"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
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
    // Prefer Postgres if DSN provided via env POSTGRES_DSN, otherwise use in-memory.
    var reg ports.InstanceRegistry
    dsn := os.Getenv("POSTGRES_DSN")
    if dsn != "" {
        pg, err := persistence.NewPostgresRegistry(dsn)
        if err != nil {
            log.Fatalf("failed to open postgres registry: %v", err)
        }
        reg = pg
    } else {
        reg = persistence.NewInMemoryRegistry()
    }
    svc := service.NewMonitorService(reg)

    // Start schedulers (pass service to allow marking inactive)
    monitor.StartSchedulers(ctx, cfg, svc)

    // Start HTTP REST server for registration
    go func() {
        if err := httpadapter.StartHTTPServer(ctx, svc, ":8080"); err != nil && err != http.ErrServerClosed {
            log.Fatalf("HTTP server exited: %v", err)
        }
    }()

    // Start gRPC server in background (kept for future gRPC support)
    go func() {
        if err := monitor.StartGRPCServer(ctx, ":50051", svc); err != nil {
            log.Fatalf("gRPC server exited: %v", err)
        }
    }()

    // Wait for shutdown signal
    <-ctx.Done()
    // give background tasks a moment to exit
    time.Sleep(500 * time.Millisecond)
    log.Println("MonitorS shut down")
}
