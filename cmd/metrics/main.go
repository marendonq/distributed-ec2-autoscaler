package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "os"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    pb "github.com/marendonq/distributed-ec2-autoscaler/api/proto/monitor"
)

func getEnvOrDefault(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}
func main() {
    monitorSAddr := flag.String("server", getEnvOrDefault("MONITOR_S_ADDR", "localhost:50051"), "MonitorS address")
    watch := flag.Bool("watch", false, "Watch continuously")
    interval := flag.Duration("interval", 10*time.Second, "Watch interval")
    flag.Parse()

    conn, err := grpc.Dial(*monitorSAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatalf("failed to connect: %v", err)
    }
    defer conn.Close()

    client := pb.NewMonitorSServiceClient(conn)

    if *watch {
        watchMetrics(client, *interval)
    } else {
        getOnce(client)
    }
}

func getOnce(client pb.MonitorSServiceClient) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    resp, err := client.GetAggregatedMetrics(ctx, &pb.AggregatedMetricsRequest{})
    if err != nil {
        log.Fatalf("failed to get metrics (gRPC error): %v", err)
    }
    if !resp.Success {
        log.Fatalf("failed to get metrics (server returned success=false)")
    }

    printMetrics(resp)
}

func watchMetrics(client pb.MonitorSServiceClient, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            resp, err := client.GetAggregatedMetrics(ctx, &pb.AggregatedMetricsRequest{})
            cancel()
            
            if err == nil && resp.Success {
                fmt.Printf("\n=== %s ===\n", time.Now().Format("15:04:05"))
                printMetrics(resp)
            } else if err != nil {
                log.Printf("watch: failed to get metrics: %v", err)
            } else {
                log.Printf("watch: metrics request returned success=false")
            }
        }
    }
}

func printMetrics(resp *pb.AggregatedMetricsResponse) {
    fmt.Printf("Avg CPU Load: %.1f%%\n", resp.AvgCpuLoad)
    fmt.Printf("Active: %d | Inactive: %d | Total: %d\n", 
        resp.ActiveInstances, resp.InactiveInstances, resp.TotalInstances)
    fmt.Println("Instances:")
    for _, inst := range resp.Instances {
        fmt.Printf("  - %s [%s]: %.1f%%\n", inst.Id, inst.Status, inst.CpuLoad)
    }
}
