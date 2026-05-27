package persistence

import (
    "os"
    "testing"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
)

// TestPostgresRegisterSetsLastSeen verifies that Register() will set LastSeen
// to a non-zero value and set Status to active when called with zero values.
func TestPostgresRegisterSetsLastSeen(t *testing.T) {
    dsn := os.Getenv("POSTGRES_DSN_TEST")
    if dsn == "" {
        dsn = os.Getenv("POSTGRES_DSN")
    }
    if dsn == "" {
        t.Skip("POSTGRES_DSN_TEST or POSTGRES_DSN not set; skipping integration test")
    }

    regIface, err := NewPostgresRegistry(dsn)
    if err != nil {
        t.Fatalf("NewPostgresRegistry error: %v", err)
    }

    // we expect the concrete type to be available in this package
    pr, ok := regIface.(*PostgresRegistry)
    if !ok {
        t.Fatalf("unexpected registry type")
    }

    id := "test-inst-" + time.Now().Format("20060102150405")
    inst := &domain.Instance{ID: id, Hostname: "test-host", IP: "127.0.0.1"}

    // ensure cleanup
    defer pr.db.Exec("DELETE FROM instances WHERE id=$1", id)

    if err := pr.Register(inst); err != nil {
        t.Fatalf("Register error: %v", err)
    }

    got, err := pr.GetByID(id)
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
    if err := pr.Register(&domain.Instance{ID: id}); err != nil {
        t.Fatalf("second Register error: %v", err)
    }
    got2, err := pr.GetByID(id)
    if err != nil {
        t.Fatalf("GetByID after second register error: %v", err)
    }
    if got2.LastSeen <= prev {
        t.Fatalf("expected LastSeen to increase after heartbeat; prev=%d now=%d", prev, got2.LastSeen)
    }
}
