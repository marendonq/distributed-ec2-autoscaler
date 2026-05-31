package main

import (
    "context"
    "flag"
    "log"
    "net"
    "os"
    "os/signal"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    pb "github.com/marendonq/distributed-ec2-autoscaler/api/proto/monitor"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/cloud"
)

func getLocalIP() string {
    conn, err := net.Dial("udp", "8.8.8.8:80")
    if err != nil {
        return "127.0.0.1"
    }
    defer conn.Close()
    localAddr := conn.LocalAddr().(*net.UDPAddr)
    return localAddr.IP.String()
}

// Global state for MonitorC
type monitorCServer struct {
    pb.UnimplementedMonitorCServiceServer
    instanceID string
}

func (s *monitorCServer) Ping(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
    // Ping sencillo para HU-02 (Vivacidad)
    return &pb.PingResponse{Success: true}, nil
}

func main() {
    monitorSAddr := flag.String("server", "localhost:50051", "MonitorS gRPC server address")
    listenAddr := flag.String("listen", ":50052", "Port to listen for MonitorS polling")
    id := flag.String("id", "", "Instance ID (defaults to hostname)")
    flag.Parse()

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

    ip := getLocalIP()
    instID := *id
    if md, err := cloud.GetInstanceMetadata(); err == nil {
        if md.InstanceID != "" {
            instID = md.InstanceID
        }
        if md.LocalIPv4 != "" {
            ip = md.LocalIPv4
        }
    }

    // 1. Iniciar servidor gRPC para que MonitorS nos haga polling
    lis, err := net.Listen("tcp", *listenAddr)
    if err != nil {
        log.Fatalf("failed to listen: %v", err)
    }
    
    srv := grpc.NewServer()
    serverImpl := &monitorCServer{
        instanceID: instID,
    }
    pb.RegisterMonitorCServiceServer(srv, serverImpl)
    
    go func() {
        log.Printf("MonitorC listening on %s", *listenAddr)
        if err := srv.Serve(lis); err != nil {
            log.Fatalf("failed to serve: %v", err)
        }
    }()

    // 2. Registrarse en MonitorS
    conn, err := grpc.Dial(*monitorSAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatalf("did not connect to MonitorS: %v", err)
    }
    defer conn.Close()
    
    client := pb.NewMonitorSServiceClient(conn)

    // Extraer puerto numérico para enviar a MonitorS
    _, portStr, _ := net.SplitHostPort(*listenAddr)
    port := 50052
    if p, err := net.LookupPort("tcp", portStr); err == nil {
        port = p
    }

    req := &pb.RegisterRequest{
        InstanceId: instID,
        Hostname:   hostname,
        LocalIp:    ip,
        GrpcPort:   int32(port),
        Meta:       map[string]string{"env": "local"},
    }
    
    // Reintentar registro si MonitorS no está arriba aún
    for {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        resp, err := client.Register(ctx, req)
        cancel()
        
        if err == nil && resp.Success {
            log.Printf("Successfully registered with MonitorS: %s", resp.Message)
            break
        }
        log.Printf("Failed to register with MonitorS (retrying in 5s): %v", err)
        time.Sleep(5 * time.Second)
    }

    // Esperar señal de salida
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()
    
    <-ctx.Done()
    log.Println("Shutting down MonitorC. Deregistering...")
    
    deregReq := &pb.DeregisterRequest{InstanceId: instID}
    ctxDereg, cancelDereg := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancelDereg()
    
    client.Deregister(ctxDereg, deregReq)
    srv.GracefulStop()
}
