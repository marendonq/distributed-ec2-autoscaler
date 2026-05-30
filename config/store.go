package config

import (
    "encoding/json"
    "fmt"
    "io/ioutil"
    "os"
)

// LoadConfig reads the configuration from the provided path. If the file
// does not exist, it writes a default configuration to that path and returns it.
func LoadConfig(path string) (*Config, error) {
    _, err := os.Stat(path)
    if os.IsNotExist(err) {
        cfg := DefaultConfig()
        if err := SaveConfig(path, cfg); err != nil {
            return nil, err
        }
        if err := Validate(cfg); err != nil {
            return nil, fmt.Errorf("config validation failed: %w", err)
        }
        return cfg, nil
    } else if err != nil {
        return nil, err
    }

    data, err := ioutil.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var cfg Config
    if err := json.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    if err := Validate(&cfg); err != nil {
        return nil, fmt.Errorf("config validation failed: %w", err)
    }
    return &cfg, nil
}

// SaveConfig persists the configuration to the provided path.
func SaveConfig(path string, cfg *Config) error {
    data, err := json.MarshalIndent(cfg, "", "  ")
    if err != nil {
        return err
    }
    return ioutil.WriteFile(path, data, 0644)
}