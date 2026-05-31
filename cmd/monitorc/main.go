package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	pb "github.com/marendonq/distributed-ec2-autoscaler/api/proto/monitor"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/cloud"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/simulation"
)

const (
	initialBackoff = 2 * time.Second
	maxBackoff     = 30 * time.Second
)

type dialContextFn func(ctx context.Context, target string, opts ...grpc.DialOption) (*grpc.ClientConn, error)

var dialFn dialContextFn = grpc.DialContext

type activeConnection struct {
	mu     sync.Mutex
	client pb.MonitorSServiceClient
}

func (a *activeConnection) set(client pb.MonitorSServiceClient) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = client
}

func (a *activeConnection) get() pb.MonitorSServiceClient {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client
}

type MonitorCConfig struct {
	MonitorSAddr string
	ListenAddr   string
	InstanceID   string
	Hostname     string
	LocalIP      string
	ListenPort   int
	Environment  string
	LoadSim      simulationParams
}

type simulationParams struct {
	Min, Max, NoiseAmp     float64
	PeriodSec, UpdateSec   int
}

func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func monitorSAddrFromEnv() string {
	if v := os.Getenv("MONITOR_S_ADDR"); v != "" {
		return v
	}
	if ip := os.Getenv("MONITOR_S_IP"); ip != "" {
		return fmt.Sprintf("%s:50051", ip)
	}
	return "localhost:50051"
}

func loadSimulationParams() simulationParams {
	return simulationParams{
		Min:       envFloat("LOAD_SIM_MIN", 10),
		Max:       envFloat("LOAD_SIM_MAX", 90),
		PeriodSec: envInt("LOAD_SIM_PERIOD_SECONDS", 120),
		NoiseAmp:  envFloat("LOAD_SIM_NOISE_AMPLITUDE", 5),
		UpdateSec: envInt("LOAD_SIM_UPDATE_INTERVAL_SECONDS", 5),
	}
}

func loadMonitorCConfig() (*MonitorCConfig, error) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}

	localIP := getLocalIP()
	instanceID := getEnvOrDefault("INSTANCE_ID", "")

	if md, err := cloud.GetInstanceMetadata(); err == nil {
		if instanceID == "" && md.InstanceID != "" {
			instanceID = md.InstanceID
		}
		if md.LocalIPv4 != "" {
			localIP = md.LocalIPv4
		}
	}
	if instanceID == "" {
		instanceID = hostname
	}

	listenAddr := getEnvOrDefault("MONITOR_C_LISTEN_ADDR", ":50052")
	listenPort := 50052
	if _, portStr, err := net.SplitHostPort(listenAddr); err == nil {
		if p, err := net.LookupPort("tcp", portStr); err == nil && p > 0 {
			listenPort = p
		}
	}

	return &MonitorCConfig{
		MonitorSAddr: monitorSAddrFromEnv(),
		ListenAddr:   listenAddr,
		InstanceID:   instanceID,
		Hostname:     hostname,
		LocalIP:      localIP,
		ListenPort:   listenPort,
		Environment:  getEnvOrDefault("ENV", "production"),
		LoadSim:      loadSimulationParams(),
	}, nil
}

func connectAndRegister(ctx context.Context, cfg *MonitorCConfig) (*grpc.ClientConn, pb.MonitorSServiceClient, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := dialFn(dialCtx, cfg.MonitorSAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to MonitorS: %w", err)
	}

	client := pb.NewMonitorSServiceClient(conn)

	req := &pb.RegisterRequest{
		InstanceId: cfg.InstanceID,
		Hostname:   cfg.Hostname,
		LocalIp:    cfg.LocalIP,
		GrpcPort:   int32(cfg.ListenPort),
		Meta: map[string]string{
			"env":         cfg.Environment,
			"created_at":  fmt.Sprintf("%d", time.Now().Unix()),
		},
	}

	regCtx, regCancel := context.WithTimeout(ctx, 5*time.Second)
	defer regCancel()

	resp, err := client.Register(regCtx, req)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("registration failed: %w", err)
	}
	if !resp.Success {
		conn.Close()
		return nil, nil, fmt.Errorf("registration rejected: %s", resp.Message)
	}

	return conn, client, nil
}

func monitorConnection(ctx context.Context, conn *grpc.ClientConn) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state := conn.GetState()
			if state != connectivity.Idle && state != connectivity.Ready {
				log.Printf("[%s] connection to MonitorS lost (state: %s)", domain.EventMonitorCConnectionLost, state)
				return
			}
		}
	}
}

func runReconnector(ctx context.Context, cfg *MonitorCConfig, active *activeConnection) {
	backoff := initialBackoff
	retries := 0

	for {
		if ctx.Err() != nil {
			return
		}

		conn, client, err := connectAndRegister(ctx, cfg)
		if err != nil {
			log.Printf("[%s] %v (retry %d, backoff %s)", domain.EventMonitorCRegistrationFailed, err, retries+1, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(time.Duration(float64(backoff)*1.5), maxBackoff)
			retries++
			continue
		}

		if retries > 0 {
			log.Printf("[%s] reconnected to MonitorS after %d retries", domain.EventMonitorCReconnected, retries)
		} else {
			log.Printf("Successfully registered with MonitorS")
		}

		active.set(client)
		backoff = initialBackoff
		retries = 0

		monitorConnection(ctx, conn)
		active.set(nil)
		conn.Close()

		if ctx.Err() != nil {
			return
		}
	}
}

type monitorCServer struct {
	pb.UnimplementedMonitorCServiceServer
	instanceID   string
	simulator    *simulation.LoadSimulator
	mu           sync.RWMutex
	shuttingDown bool
	stopSim      chan struct{}
	stopOnce     sync.Once
}

func newMonitorCServer(cfg *MonitorCConfig) *monitorCServer {
	s := &monitorCServer{
		instanceID: cfg.InstanceID,
		simulator: simulation.NewLoadSimulator(
			cfg.InstanceID,
			cfg.LoadSim.Min,
			cfg.LoadSim.Max,
			cfg.LoadSim.PeriodSec,
			cfg.LoadSim.NoiseAmp,
		),
		stopSim: make(chan struct{}),
	}
	s.simulator.Tick()
	go s.runSimulationLoop(cfg.LoadSim.UpdateSec)
	return s
}

func (s *monitorCServer) runSimulationLoop(updateSec int) {
	if updateSec <= 0 {
		updateSec = 5
	}
	ticker := time.NewTicker(time.Duration(updateSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopSim:
			return
		case <-ticker.C:
			s.mu.RLock()
			stopped := s.shuttingDown
			s.mu.RUnlock()
			if stopped {
				return
			}
			s.simulator.Tick()
		}
	}
}

func (s *monitorCServer) Ping(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.shuttingDown {
		return &pb.PingResponse{Success: false}, nil
	}
	return &pb.PingResponse{Success: true}, nil
}

func (s *monitorCServer) GetMetrics(ctx context.Context, req *pb.GetMetricsRequest) (*pb.GetMetricsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.shuttingDown {
		return &pb.GetMetricsResponse{Success: false, InstanceId: s.instanceID}, nil
	}
	load := float32(s.simulator.Current())
	return &pb.GetMetricsResponse{
		Success:    true,
		CpuLoad:    load,
		Timestamp:  time.Now().Unix(),
		InstanceId: s.instanceID,
	}, nil
}

func (s *monitorCServer) Shutdown(ctx context.Context, req *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	s.mu.Lock()
	s.shuttingDown = true
	s.mu.Unlock()
	s.stopOnce.Do(func() { close(s.stopSim) })
	log.Printf("MonitorC shutdown requested for instance %s", s.instanceID)
	return &pb.ShutdownResponse{Success: true, InstanceId: s.instanceID}, nil
}

func main() {
	monitorSAddr := flag.String("server", monitorSAddrFromEnv(), "MonitorS gRPC server address")
	listenAddr := flag.String("listen", getEnvOrDefault("MONITOR_C_LISTEN_ADDR", ":50052"), "Port to listen for MonitorS polling")
	id := flag.String("id", getEnvOrDefault("INSTANCE_ID", ""), "Instance ID (defaults to hostname)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// HU-10: Dynamic MonitorS discovery
	if *monitorSAddr == "localhost:50051" {
		if discoveredIP := cloud.DiscoverMonitorS(); discoveredIP != "" {
			*monitorSAddr = discoveredIP + ":50051"
			log.Printf("HU-10: Usando MonitorS descubierto dinámicamente: %s", *monitorSAddr)
		}
	}

	hostname, _ := os.Hostname()
	if *id == "" {
		*id = hostname
	}

	cfg, err := loadMonitorCConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	cfg.MonitorSAddr = *monitorSAddr
	cfg.ListenAddr = *listenAddr
	if *id != "" {
		cfg.InstanceID = *id
	}

	cfg.ListenPort = 50052
	if _, portStr, err := net.SplitHostPort(cfg.ListenAddr); err == nil {
		if p, err := net.LookupPort("tcp", portStr); err == nil && p > 0 {
			cfg.ListenPort = p
		}
	}

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	monitorSrv := newMonitorCServer(cfg)
	srv := grpc.NewServer()
	pb.RegisterMonitorCServiceServer(srv, monitorSrv)

	go func() {
		log.Printf("MonitorC listening on %s (simulated load)", cfg.ListenAddr)
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	active := &activeConnection{}
	go runReconnector(ctx, cfg, active)

	<-ctx.Done()
	log.Println("Shutting down MonitorC. Deregistering...")

	if client := active.get(); client != nil {
		deregCtx, deregCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = client.Deregister(deregCtx, &pb.DeregisterRequest{InstanceId: cfg.InstanceID})
		deregCancel()
	}

	srv.GracefulStop()
}
