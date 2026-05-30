package persistence

import (
    "os"
    "testing"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
)

func TestSQLiteRegisterSetsLastSeen(t *testing.T) {
    dbPath := "teleproy2_test.db"
    defer os.Remove(dbPath)

    regIface, err := NewSQLiteRegistry(dbPath)
    if err != nil {
        t.Fatalf("NewSQLiteRegistry error: %v", err)
    }

    sr, ok := regIface.(*SQLiteRegistry)
    if !ok {
        t.Fatalf("unexpected registry type")
    }
    defer sr.db.Close()

    id := "test-inst-" + time.Now().Format("20060102150405")
    inst := &domain.Instance{ID: id, Hostname: "test-host", IP: "127.0.0.1"}

    if err := sr.Register(inst); err != nil {
        t.Fatalf("Register error: %v", err)
    }

    got, err := sr.GetByID(id)
    if err != nil {
        t.Fatalf("GetByID error: %v", err)
    }
    if got.LastSeen == 0 {
        t.Fatalf("expected LastSeen > 0, got 0")
    }
    if got.Status != domain.StatusActive {
        t.Fatalf("expected Status active, got %v", got.Status)
    }

    // call Register again to simulate heartbeat and ensure LastSeen is updated
    prev := got.LastSeen
    time.Sleep(1 * time.Second)
    if err := sr.Register(&domain.Instance{ID: id}); err != nil {
        t.Fatalf("second Register error: %v", err)
    }
    got2, err := sr.GetByID(id)
    if err != nil {
        t.Fatalf("GetByID after second register error: %v", err)
    }
    if got2.LastSeen <= prev {
        t.Fatalf("expected LastSeen to increase after heartbeat; prev=%d now=%d", prev, got2.LastSeen)
    }
}
