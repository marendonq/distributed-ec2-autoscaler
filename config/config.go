package config
import (
    "fmt"
    "strings"
)


type EC2Params struct {
    AMI            string   `json:"ami"`
    InstanceType   string   `json:"instance_type"`
    KeyName        string   `json:"key_name"`
    SecurityGroups []string `json:"security_groups"`
    SubnetID       string   `json:"subnet_id"`
    Tags           map[string]string `json:"tags"`
}

type ScalingPolicy struct {
    Name      string `json:"name"`
    Type      string `json:"type"`
    Threshold int    `json:"threshold"`
}

type Config struct {
    MinInstances int            `json:"min_instances"`
    MaxInstances int            `json:"max_instances"`
    EC2Params    EC2Params      `json:"ec2_params"`
    Policies     []ScalingPolicy `json:"policies"`
    Region            string   `json:"region"` 
    MonitorSIP      string   `json:"monitor_s_ip"`
    MonitorSPort    int   `json:"monitor_s_port"`
    MonitorCPort    int   `json:"monitor_c_port"`
    // Heartbeat settings (seconds)
    HeartbeatCheckIntervalSeconds int `json:"heartbeat_check_interval_seconds"`
    HeartbeatTimeoutSeconds       int `json:"heartbeat_timeout_seconds"`
}

func DefaultConfig() *Config {
    return &Config{
        MinInstances: 2,
        MaxInstances: 5,
        Region: "us-east-1",
        MonitorSIP: "<MONITOR_S_PRIVATE_IP_ADDRESS>",
        MonitorSPort: 50052,
        MonitorCPort: 50051,
        EC2Params: EC2Params{
            AMI:            "REPLACE_WITH_AMI_ID",
            InstanceType:   "t2.micro",
            KeyName:        "",
            SecurityGroups: []string{},
            SubnetID:      "",
            Tags:           map[string]string{}, 
        },
        Policies: []ScalingPolicy{},
        HeartbeatCheckIntervalSeconds: 10,
        HeartbeatTimeoutSeconds:       30,
    }
}

func Validate(cfg *Config) error {
    if cfg.Region == "" || strings.Contains(cfg.Region, "REPLACE_WITH") {
    return fmt.Errorf("Region is empty or contains placeholder: %q", cfg.Region)
}

    if cfg.MonitorSIP == "" || strings.Contains(cfg.MonitorSIP, "REPLACE_WITH") {
    return fmt.Errorf("MonitorSIP is empty or contains placeholder: %q", cfg.MonitorSIP)
}

    if cfg.EC2Params.AMI == "" || strings.Contains(cfg.EC2Params.AMI, "REPLACE_WITH") {
    return fmt.Errorf("EC2Params.AMI is empty or contains placeholder: %q", cfg.EC2Params.AMI)
}

    if cfg.EC2Params.SubnetID == "" || strings.Contains(cfg.EC2Params.SubnetID, "REPLACE_WITH") {
    return fmt.Errorf("EC2Params.SubnetID is empty or contains placeholder: %q", cfg.EC2Params.SubnetID)
}

    if len(cfg.EC2Params.SecurityGroups) == 0 {
    return fmt.Errorf("EC2Params.SecurityGroups must contain at least one SG")}
        for _, sg := range cfg.EC2Params.SecurityGroups {
    if strings.Contains(sg, "REPLACE_WITH") {
        return fmt.Errorf("EC2Params.SecurityGroups contains placeholder: %q", sg)
    }
}

    if cfg.MinInstances < 2 {
    return fmt.Errorf("MinInstances must be >= 2, got %d", cfg.MinInstances)
}

    if cfg.MaxInstances < cfg.MinInstances {
    return fmt.Errorf("MaxInstances must be >= MinInstances (%d), got %d", cfg.MinInstances, cfg.MaxInstances)
}

    return nil
}