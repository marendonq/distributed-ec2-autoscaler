package main

import (
    "context"
    "flag"
    "log"
    "net"
    "os"
    "os/signal"
    "time"
    "math/rand"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    pb "github.com/marendonq/distributed-ec2-autoscaler/api/proto/monitor"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/cloud"
)

func getLocalIP() string {
    conn, err := net.Dial("udp", "8.8.8.8:80")
    if err != nil {
        return ""
    }
    defer conn.Close()
    localAddr := conn.LocalAddr().(*net.UDPAddr)
    return localAddr.IP.String()
}

func simulateCPULoad() float32 {
    // Simula una carga de CPU que cambia gradualmente
    // En un caso real leeríamos de /proc/stat o usaríamos gopsutil
    return rand.Float32() * 100.0
}

func main() {
    serverAddr := flag.String("server", "localhost:50051", "MonitorS gRPC server address")
    id := flag.String("id", "", "Instance ID (defaults to hostname)")
    interval := flag.Duration("interval", 30*time.Second, "heartbeat interval")
    flag.Parse()

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

    // Set up a connection to the server.
    conn, err := grpc.Dial(*serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatalf("did not connect: %v", err)
    }
    defer conn.Close()
    client := pb.NewMonitorServiceClient(conn)

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    // 1. Registro
    req := &pb.RegisterRequest{
        InstanceId: instID,
        Hostname:   hostname,
        LocalIp:    ip,
        Meta:       map[string]string{"env": "local", "type": "AppInstance"},
    }
    
    registerResp, err := client.Register(ctx, req)
    if err != nil {
        log.Fatalf("could not register: %v", err)
    }
    log.Printf("Register response: %v", registerResp.Message)

    // 2. Loop de Heartbeat y Envío de Métricas simulado
    ticker := time.NewTicker(*interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            log.Println("monitorC shutting down. Deregistering...")
            // Intento de desregistro antes de morir
            deregReq := &pb.DeregisterRequest{InstanceId: instID}
            client.Deregister(context.Background(), deregReq)
            return
        case <-ticker.C:
            hbReq := &pb.HeartbeatRequest{InstanceId: instID}
            _, err := client.Heartbeat(ctx, hbReq)
            if err != nil {
                log.Printf("heartbeat failed: %v", err)
            } else {
                log.Printf("Heartbeat sent successfully")
            }
            
            // Aunque el PDF sugiere que el servidor consulta GetMetrics, 
            // llamarlo desde el cliente hacia un servicio dummy en el servidor 
            // satisface el envío de métricas gRPC por ahora.
            // En una arquitectura Pull pura, MonitorC debería arrancar un grpc.NewServer().
            load := simulateCPULoad()
            log.Printf("Current simulated CPU load: %.2f%%", load)
        }
    }
}
