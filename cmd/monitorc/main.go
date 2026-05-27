package main

import (
    "bytes"
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "log"
    "net"
    "net/http"
    "os"
    "os/signal"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/cloud"
)

func getLocalIP() string {
    // Dial UDP to obtain a non-loopback local IP
    conn, err := net.Dial("udp", "8.8.8.8:80")
    if err != nil {
        return ""
    }
    defer conn.Close()
    localAddr := conn.LocalAddr().(*net.UDPAddr)
    return localAddr.IP.String()
}

func registerOnce(server string, inst *domain.Instance, client *http.Client) error {
    url := fmt.Sprintf("%s/register", server)
    data, err := json.Marshal(inst)
    if err != nil {
        return err
    }
    req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
    if err != nil {
        return err
    }
    req.Header.Set("Content-Type", "application/json")
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        return nil
    }
    return fmt.Errorf("unexpected status %d", resp.StatusCode)
}

func main() {
    server := flag.String("server", "http://localhost:8080", "MonitorS server base URL")
    id := flag.String("id", "", "Instance ID (defaults to hostname)")
    interval := flag.Duration("interval", 30*time.Second, "re-register interval")
    retry := flag.Duration("retry", 5*time.Second, "retry interval on failure")
    flag.Parse()

    hostname, _ := os.Hostname()
    if *id == "" {
        *id = hostname
    }

    // Try to read EC2 metadata (IMDSv2). If present, use instance-id and local IPv4.
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

    inst := &domain.Instance{
        ID:       instID,
        Hostname: hostname,
        IP:       ip,
        Meta:     map[string]string{"env": "local"},
    }

    client := &http.Client{Timeout: 10 * time.Second}

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    // initial register with retries
    for {
        err := registerOnce(*server, inst, client)
        if err == nil {
            log.Printf("registered instance %s with %s", inst.ID, *server)
            break
        }
        log.Printf("register failed: %v; retrying in %s", err, retry)
        select {
        case <-time.After(*retry):
            continue
        case <-ctx.Done():
            log.Println("shutting down before successful register")
            return
        }
    }

    // periodic re-register (heartbeat via registration)
    ticker := time.NewTicker(*interval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            log.Println("monitorC shutting down")
            return
        case <-ticker.C:
            // update some metadata if desired
            inst.Meta["last_register"] = time.Now().Format(time.RFC3339)
            if err := registerOnce(*server, inst, client); err != nil {
                log.Printf("re-register failed: %v", err)
            } else {
                log.Printf("re-registered %s", inst.ID)
            }
        }
    }
}
