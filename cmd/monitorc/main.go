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
    "strings"
    "sync"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/connectivity"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/grpc/keepalive"

    pb "github.com/marendonq/distributed-ec2-autoscaler/api/proto/monitor"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/cloud"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
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
}

func getLocalIP() string {
    conn, err := net.Dial("udp", "8.8.8.8:80")
    if err != nil {
        return "127.0.0.1"
    }
    defer conn.Close()
    localAddr := conn.LocalAddr().(*net.UDPAddr)
    return localAddr.IP.String()
}

func getEnvOrDefault(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
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
        MonitorSAddr: getEnvOrDefault("MONITOR_S_ADDR", "localhost:50051"),
        ListenAddr:   listenAddr,
        InstanceID:   instanceID,
        Hostname:     hostname,
        LocalIP:      localIP,
        ListenPort:   listenPort,
        Environment:  getEnvOrDefault("ENV", "production"),
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
        Meta:       map[string]string{"env": cfg.Environment},
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

func monitorConnection(ctx context.Context, conn *grpc.ClientConn, client pb.MonitorSServiceClient, cfg *MonitorCConfig) {
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

        monitorConnection(ctx, conn, client, cfg)
        active.set(nil)
        conn.Close()

        if ctx.Err() != nil {
            return
        }
    }
}

// Global state for MonitorC
type monitorCServer struct {
    pb.UnimplementedMonitorCServiceServer
    instanceID string
}

func (s *monitorCServer) Ping(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
    return &pb.PingResponse{Success: true}, nil
}

func getCPULoad() (float32, error) {
    data, err := os.ReadFile("/proc/stat")
    if err != nil {
        return 0, err
    }

    lines := strings.Split(strings.TrimSpace(string(data)), "\n")
    if len(lines) == 0 {
        return 0, fmt.Errorf("no cpu stats found")
    }

    fields := strings.Fields(lines[0])
    if len(fields) < 8 {
        return 0, fmt.Errorf("invalid cpu stat format")
    }

    var values [8]float32
    for i := 0; i < 8; i++ {
        v, _ := strconv.ParseFloat(fields[i+1], 32)
        values[i] = float32(v)
    }

    idle := values[3]
    total := values[0] + values[1] + values[2] + values[3] + values[4] + values[5] + values[6] + values[7]

    if total == 0 {
        return 0, nil
    }

    load := ((total - idle) / total) * 100
    if load < 0 {
        load = 0
    }
    if load > 100 {
        load = 100
    }

    return load, nil
}

func (s *monitorCServer) GetMetrics(ctx context.Context, req *pb.GetMetricsRequest) (*pb.GetMetricsResponse, error) {
    load, err := getCPULoad()
    if err != nil {
        log.Printf("GetMetrics error: %v", err)
        return &pb.GetMetricsResponse{Success: false}, nil
    }
    return &pb.GetMetricsResponse{
        Success:   true,
        CpuLoad:   load,
        Timestamp: time.Now().Unix(),
    }, nil
}

func main() {
    monitorSAddr := flag.String("server", getEnvOrDefault("MONITOR_S_ADDR", "localhost:50051"), "MonitorS gRPC server address")
    listenAddr := flag.String("listen", getEnvOrDefault("MONITOR_C_LISTEN_ADDR", ":50052"), "Port to listen for MonitorS polling")
    id := flag.String("id", getEnvOrDefault("INSTANCE_ID", ""), "Instance ID (defaults to hostname)")
    flag.Parse()

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

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

    srv := grpc.NewServer()
    pb.RegisterMonitorCServiceServer(srv, &monitorCServer{instanceID: cfg.InstanceID})

    go func() {
        log.Printf("MonitorC listening on %s", cfg.ListenAddr)
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
        client.Deregister(deregCtx, &pb.DeregisterRequest{InstanceId: cfg.InstanceID})
        deregCancel()
    }

    srv.GracefulStop()
}
