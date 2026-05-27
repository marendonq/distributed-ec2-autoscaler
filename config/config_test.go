package config

import (
    "io/ioutil"
    "os"
    "path/filepath"
    "testing"
)

func TestSaveLoadConfig(t *testing.T) {
    tmpdir, err := ioutil.TempDir("", "configtest")
    if err != nil {
        t.Fatal(err)
    }
    defer os.RemoveAll(tmpdir)
    path := filepath.Join(tmpdir, "config.json")
    cfg := &Config{
        MinInstances: 2,
        MaxInstances: 4,
        EC2Params: EC2Params{
            AMI:          "ami-test",
            InstanceType: "t2.small",
            KeyName:      "key",
            SecurityGroups: []string{"sg-1"},
        },
        Policies: []ScalingPolicy{
            {Name: "scale-up", Type: "cpu", Threshold: 80},
        },
    }
    if err := SaveConfig(path, cfg); err != nil {
        t.Fatalf("SaveConfig failed: %v", err)
    }
    loaded, err := LoadConfig(path)
    if err != nil {
        t.Fatalf("LoadConfig failed: %v", err)
    }
    if loaded.MinInstances != cfg.MinInstances || loaded.MaxInstances != cfg.MaxInstances {
        t.Fatalf("Loaded mismatch: %#v", loaded)
    }
}
