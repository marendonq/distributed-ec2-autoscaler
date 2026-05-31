package config
import (
    "encoding/json"
    "fmt"
    "os"
    "strconv"
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

type LoadSimulation struct {
    Min                   float64 `json:"min"`
    Max                   float64 `json:"max"`
    PeriodSeconds         int     `json:"period_seconds"`
    NoiseAmplitude        float64 `json:"noise_amplitude"`
    UpdateIntervalSeconds int     `json:"update_interval_seconds"`
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
    HeartbeatCheckIntervalSeconds int `json:"heartbeat_check_interval_seconds"`
    HeartbeatTimeoutSeconds       int `json:"heartbeat_timeout_seconds"`
    GRPCTimeoutSeconds            int `json:"grpc_timeout_seconds"`
    ScaleUpThreshold              float64 `json:"scale_up_threshold"`
    ScaleDownThreshold            float64 `json:"scale_down_threshold"`
    EvaluationWindow              int     `json:"evaluation_window"`
    CooldownSeconds               int     `json:"cooldown_seconds"`
    LoadSimulation                LoadSimulation `json:"load_simulation"`
}

func DefaultConfig() *Config {
    return &Config{
        MinInstances: 2,
        MaxInstances: 5,
        Region: "us-east-1",
        MonitorSIP: "<MONITOR_S_PRIVATE_IP_ADDRESS>",
        MonitorSPort: 50051,
        MonitorCPort: 50052,
        EC2Params: EC2Params{
            AMI:            "REPLACE_WITH_AMI_ID",
            InstanceType:   "t2.micro",
            KeyName:        "",
            SecurityGroups: []string{},
            SubnetID:      "",
            Tags: map[string]string{
                "ManagedBy": "ControllerASG",
                "Project":   "ASG-Project2",
            },
        },
        Policies: []ScalingPolicy{},
        HeartbeatCheckIntervalSeconds: 10,
        HeartbeatTimeoutSeconds:       30,
        GRPCTimeoutSeconds:            3,
        ScaleUpThreshold:              70,
        ScaleDownThreshold:            30,
        EvaluationWindow:              3,
        CooldownSeconds:               180,
        LoadSimulation: LoadSimulation{
            Min:                   10,
            Max:                   90,
            PeriodSeconds:         120,
            NoiseAmplitude:        5,
            UpdateIntervalSeconds: 5,
        },
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

    if cfg.EvaluationWindow < 1 {
        cfg.EvaluationWindow = 3
    }
    if cfg.CooldownSeconds < 0 {
        cfg.CooldownSeconds = 180
    }
    if cfg.GRPCTimeoutSeconds <= 0 {
        cfg.GRPCTimeoutSeconds = 3
    }
    if cfg.ScaleUpThreshold <= 0 {
        cfg.ScaleUpThreshold = 70
    }
    if cfg.ScaleDownThreshold <= 0 {
        cfg.ScaleDownThreshold = 30
    }
    if cfg.LoadSimulation.UpdateIntervalSeconds <= 0 {
        cfg.LoadSimulation.UpdateIntervalSeconds = 5
    }
    if cfg.LoadSimulation.PeriodSeconds <= 0 {
        cfg.LoadSimulation.PeriodSeconds = 120
    }
    if cfg.LoadSimulation.Max <= cfg.LoadSimulation.Min {
        cfg.LoadSimulation.Min = 10
        cfg.LoadSimulation.Max = 90
    }

    return nil
}
func ApplyEnvOverrides(cfg *Config) error {
    if cfg == nil {
        return nil
    }
    // AUTOSCALER_MIN_INSTANCES
    if v := os.Getenv("AUTOSCALER_MIN_INSTANCES"); v != "" {
        if n, err := strconv.Atoi(v); err != nil {
            return fmt.Errorf("AUTOSCALER_MIN_INSTANCES invalid: %w", err)
        } else {
            cfg.MinInstances = n
        }
    }
    // AUTOSCALER_MAX_INSTANCES
    if v := os.Getenv("AUTOSCALER_MAX_INSTANCES"); v != "" {
        if n, err := strconv.Atoi(v); err != nil {
            return fmt.Errorf("AUTOSCALER_MAX_INSTANCES invalid: %w", err)
        } else {
            cfg.MaxInstances = n
        }
    }
    // AUTOSCALER_REGION
    if v := os.Getenv("AUTOSCALER_REGION"); v != "" {
        cfg.Region = v
    }
    // AUTOSCALER_MONITOR_S_IP
    if v := os.Getenv("AUTOSCALER_MONITOR_S_IP"); v != "" {
        cfg.MonitorSIP = v
    }
    // AUTOSCALER_MONITOR_S_PORT
    if v := os.Getenv("AUTOSCALER_MONITOR_S_PORT"); v != "" {
        if n, err := strconv.Atoi(v); err != nil {
            return fmt.Errorf("AUTOSCALER_MONITOR_S_PORT invalid: %w", err)
        } else {
            cfg.MonitorSPort = n
        }
    }
    // AUTOSCALER_MONITOR_C_PORT
    if v := os.Getenv("AUTOSCALER_MONITOR_C_PORT"); v != "" {
        if n, err := strconv.Atoi(v); err != nil {
            return fmt.Errorf("AUTOSCALER_MONITOR_C_PORT invalid: %w", err)
        } else {
            cfg.MonitorCPort = n
        }
    }
    // AUTOSCALER_HEARTBEAT_CHECK_INTERVAL_SECONDS
    if v := os.Getenv("AUTOSCALER_HEARTBEAT_CHECK_INTERVAL_SECONDS"); v != "" {
        if n, err := strconv.Atoi(v); err != nil {
            return fmt.Errorf("AUTOSCALER_HEARTBEAT_CHECK_INTERVAL_SECONDS invalid: %w", err)
        } else {
            cfg.HeartbeatCheckIntervalSeconds = n
        }
    }
    // AUTOSCALER_HEARTBEAT_TIMEOUT_SECONDS
    if v := os.Getenv("AUTOSCALER_HEARTBEAT_TIMEOUT_SECONDS"); v != "" {
        if n, err := strconv.Atoi(v); err != nil {
            return fmt.Errorf("AUTOSCALER_HEARTBEAT_TIMEOUT_SECONDS invalid: %w", err)
        } else {
            cfg.HeartbeatTimeoutSeconds = n
        }
    }
    if v := os.Getenv("AUTOSCALER_GRPC_TIMEOUT_SECONDS"); v != "" {
        if n, err := strconv.Atoi(v); err != nil {
            return fmt.Errorf("AUTOSCALER_GRPC_TIMEOUT_SECONDS invalid: %w", err)
        } else {
            cfg.GRPCTimeoutSeconds = n
        }
    }
    if v := os.Getenv("AUTOSCALER_SCALE_UP_THRESHOLD"); v != "" {
        if n, err := strconv.ParseFloat(v, 64); err != nil {
            return fmt.Errorf("AUTOSCALER_SCALE_UP_THRESHOLD invalid: %w", err)
        } else {
            cfg.ScaleUpThreshold = n
        }
    }
    if v := os.Getenv("AUTOSCALER_SCALE_DOWN_THRESHOLD"); v != "" {
        if n, err := strconv.ParseFloat(v, 64); err != nil {
            return fmt.Errorf("AUTOSCALER_SCALE_DOWN_THRESHOLD invalid: %w", err)
        } else {
            cfg.ScaleDownThreshold = n
        }
    }
    if v := os.Getenv("AUTOSCALER_EVALUATION_WINDOW"); v != "" {
        if n, err := strconv.Atoi(v); err != nil {
            return fmt.Errorf("AUTOSCALER_EVALUATION_WINDOW invalid: %w", err)
        } else {
            cfg.EvaluationWindow = n
        }
    }
    if v := os.Getenv("AUTOSCALER_COOLDOWN_SECONDS"); v != "" {
        if n, err := strconv.Atoi(v); err != nil {
            return fmt.Errorf("AUTOSCALER_COOLDOWN_SECONDS invalid: %w", err)
        } else {
            cfg.CooldownSeconds = n
        }
    }
    // AUTOSCALER_EC2_AMI
    if v := os.Getenv("AUTOSCALER_EC2_AMI"); v != "" {
        cfg.EC2Params.AMI = v
    }
    // AUTOSCALER_EC2_INSTANCE_TYPE
    if v := os.Getenv("AUTOSCALER_EC2_INSTANCE_TYPE"); v != "" {
        cfg.EC2Params.InstanceType = v
    }
    // AUTOSCALER_EC2_KEY_NAME
    if v := os.Getenv("AUTOSCALER_EC2_KEY_NAME"); v != "" {
        cfg.EC2Params.KeyName = v
    }
    // AUTOSCALER_EC2_SECURITY_GROUPS
    if v := os.Getenv("AUTOSCALER_EC2_SECURITY_GROUPS"); v != "" {
        cfg.EC2Params.SecurityGroups = strings.Split(v, ",")
    }
    // AUTOSCALER_EC2_SUBNET_ID
    if v := os.Getenv("AUTOSCALER_EC2_SUBNET_ID"); v != "" {
        cfg.EC2Params.SubnetID = v
    }
    // AUTOSCALER_EC2_TAGS
    if v := os.Getenv("AUTOSCALER_EC2_TAGS"); v != "" {
        if err := json.Unmarshal([]byte(v), &cfg.EC2Params.Tags); err != nil {
            return fmt.Errorf("AUTOSCALER_EC2_TAGS invalid JSON: %w", err)
        }
    }
    return nil
}
