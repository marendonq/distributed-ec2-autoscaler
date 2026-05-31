package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marendonq/distributed-ec2-autoscaler/config"
	httpAdapter "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/http"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/persistence"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/controller"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/service"
	"github.com/marendonq/distributed-ec2-autoscaler/pkg/monitor"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cfg, err := config.LoadConfig("config.json")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if err := config.ApplyEnvOverrides(cfg); err != nil {
		log.Fatalf("failed to apply env overrides: %v", err)
	}
	if cfg.HeartbeatCheckIntervalSeconds <= 0 {
		cfg.HeartbeatCheckIntervalSeconds = 10
	}
	if cfg.HeartbeatTimeoutSeconds <= 0 {
		cfg.HeartbeatTimeoutSeconds = 30
	}
	log.Printf("MonitorS starting with config: region=%s min=%d max=%d", cfg.Region, cfg.MinInstances, cfg.MaxInstances)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var reg ports.InstanceRegistry
	sq, err := persistence.NewSQLiteRegistry("teleproy2.db")
	if err != nil {
		log.Printf("failed to open sqlite registry, falling back to in-memory: %v", err)
		reg = persistence.NewInMemoryRegistry()
	} else {
		reg = sq
	}

	eventStore, err := persistence.NewSQLiteEventStore("teleproy2.db")
	if err != nil {
		log.Printf("[HU-11] sqlite event store unavailable, logging disabled: %v", err)
	}
	eventSvc := service.NewEventService(eventStore)

	svc := service.NewMonitorService(reg, eventSvc)

	go func() {
		if err := httpAdapter.StartHTTPServerWithEventService(ctx, svc, eventSvc, ":8080"); err != nil && err != http.ErrServerClosed {
			log.Printf("[HU-29] HTTP server error: %v", err)
		}
	}()

	asg, err := controller.NewASGController(ctx, cfg, reg, eventSvc)
	if err != nil {
		log.Fatalf("failed to initialize ControllerASG: %v", err)
	}

	grpcTimeout := time.Duration(cfg.GRPCTimeoutSeconds) * time.Second
	if grpcTimeout <= 0 {
		grpcTimeout = 3 * time.Second
	}
	monitor.StartSchedulers(ctx, cfg, svc, eventSvc, monitor.SchedulerOpts{
		GRPCTimeout:    grpcTimeout,
		MaxFailures:    3,
		OnDeadInstance: asg.HandleDeadInstance,
	})

	monitorAddr := fmt.Sprintf(":%d", cfg.MonitorSPort)
	if cfg.MonitorSPort <= 0 {
		monitorAddr = ":50051"
	}
	go func() {
		if err := monitor.StartGRPCServer(ctx, monitorAddr, svc, eventSvc); err != nil {
			log.Fatalf("gRPC server exited: %v", err)
		}
	}()

	go asg.Start(ctx)

	<-ctx.Done()
	time.Sleep(500 * time.Millisecond)
	log.Println("MonitorS shut down")
}
