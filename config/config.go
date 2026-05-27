package config

type EC2Params struct {
    AMI            string   `json:"ami"`
    InstanceType   string   `json:"instance_type"`
    KeyName        string   `json:"key_name"`
    SecurityGroups []string `json:"security_groups"`
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
    // Heartbeat settings (seconds)
    HeartbeatCheckIntervalSeconds int `json:"heartbeat_check_interval_seconds"`
    HeartbeatTimeoutSeconds       int `json:"heartbeat_timeout_seconds"`
}

func DefaultConfig() *Config {
    return &Config{
        MinInstances: 1,
        MaxInstances: 5,
        EC2Params: EC2Params{
            AMI:            "ami-0123456789abcdef0",
            InstanceType:   "t2.micro",
            KeyName:        "",
            SecurityGroups: []string{},
        },
        Policies: []ScalingPolicy{},
        HeartbeatCheckIntervalSeconds: 30,
        HeartbeatTimeoutSeconds:       90,
    }
}
