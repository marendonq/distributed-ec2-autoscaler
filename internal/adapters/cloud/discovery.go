package cloud

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// DiscoverMonitorS attempts to find the MonitorS server IP dynamically.
// HU-10: Priority order:
//  1. MONITOR_S_IP environment variable
//  2. /etc/monitor_c.env file (injected by EC2 User Data at boot)
//  3. AWS EC2 DescribeInstances query (tag Name=MonitorS*, state=running)
//
// Returns empty string if all methods fail.
func DiscoverMonitorS() string {
	// 1. Check environment variable (set by systemd EnvironmentFile or manually)
	if ip := os.Getenv("MONITOR_S_IP"); ip != "" && !isPlaceholder(ip) {
		log.Printf("HU-10: MonitorS descubierto via variable de entorno: %s", ip)
		return ip
	}

	// 2. Try reading /etc/monitor_c.env (injected by user-data.sh on EC2 boot)
	if data, err := os.ReadFile("/etc/monitor_c.env"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "MONITOR_S_IP=") {
				ip := strings.TrimPrefix(line, "MONITOR_S_IP=")
				ip = strings.Trim(ip, "\"' ")
				if ip != "" && !isPlaceholder(ip) {
					log.Printf("HU-10: MonitorS descubierto via /etc/monitor_c.env: %s", ip)
					return ip
				}
			}
		}
	}

	// 3. Try AWS SDK discovery — search for a running instance tagged as MonitorS
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	awscfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))
	if err != nil {
		log.Printf("HU-10: No se pudo cargar config AWS para descubrimiento: %v", err)
		return ""
	}

	client := ec2.NewFromConfig(awscfg)
	result, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{Name: aws.String("tag:Name"), Values: []string{"MonitorS*"}},
			{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	if err != nil {
		log.Printf("HU-10: Fallo en descubrimiento AWS: %v", err)
		return ""
	}

	for _, res := range result.Reservations {
		for _, inst := range res.Instances {
			if inst.PrivateIpAddress != nil {
				log.Printf("HU-10: MonitorS descubierto via AWS EC2: %s", *inst.PrivateIpAddress)
				return *inst.PrivateIpAddress
			}
		}
	}

	return ""
}

// isPlaceholder returns true if the string looks like an unresolved placeholder.
func isPlaceholder(s string) bool {
	return strings.Contains(s, "REPLACE_WITH") || strings.Contains(s, "<") || strings.Contains(s, ">")
}
