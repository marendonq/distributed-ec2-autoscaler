package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/marendonq/distributed-ec2-autoscaler/api/proto/monitor"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/tools"
)

func main() {
	monitorSAddr := flag.String("server", "localhost:50051", "MonitorS gRPC server address")
	instanceID := flag.String("id", "", "Instance ID")
	workers := flag.Int("workers", 0, "Number of CPU workers")
	flag.Parse()
	_ = workers

	if *instanceID == "" {
		hn, _ := os.Hostname()
		*instanceID = hn
	}

	conn, err := grpc.Dial(*monitorSAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewMonitorSServiceClient(conn)

	simulator := tools.NewLoadSimulator()
	simulator.SetTargetLoad(30.0)
	simulator.Start()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	log.Printf("Load simulator started for instance %s", *instanceID)

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down")
			simulator.Stop()
			return
		case <-ticker.C:
			load := simulator.GetLoad()
			log.Printf("Current simulated load: %.1f%%", load)

			if load > 70.0 && load < 80.0 {
				simulator.SetTargetLoad(20.0)
			} else if load < 20.0 {
				simulator.SetTargetLoad(80.0)
			}

			_ = client
		}
	}
}
