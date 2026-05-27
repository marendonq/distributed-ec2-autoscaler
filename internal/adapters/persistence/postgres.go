package persistence

import (
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    _ "github.com/lib/pq"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
)

type PostgresRegistry struct {
    db *sql.DB
}

// NewPostgresRegistry opens a postgres connection with provided DSN (e.g.
// postgres://user:pass@localhost:5432/dbname?sslmode=disable) and ensures
// the instances table exists.
func NewPostgresRegistry(dsn string) (ports.InstanceRegistry, error) {
    db, err := sql.Open("postgres", dsn)
    if err != nil {
        return nil, err
    }
    if err := db.Ping(); err != nil {
        return nil, err
    }
    r := &PostgresRegistry{db: db}
    if err := r.ensureSchema(); err != nil {
        db.Close()
        return nil, err
    }
    return r, nil
}

func (r *PostgresRegistry) ensureSchema() error {
    q := `CREATE TABLE IF NOT EXISTS instances (
        id TEXT PRIMARY KEY,
        hostname TEXT,
        ip TEXT,
        meta JSONB,
        status TEXT,
        last_seen BIGINT
    )`
    _, err := r.db.Exec(q)
    return err
}

func (r *PostgresRegistry) Register(instance *domain.Instance) error {
    meta := []byte("null")
    if instance.Meta != nil {
        b, err := json.Marshal(instance.Meta)
        if err != nil {
            return err
        }
        meta = b
    }
    // ensure last-seen and status are set when registering/heartbeating
    if instance.LastSeen == 0 {
        instance.LastSeen = time.Now().Unix()
    }
    if instance.Status == "" {
        instance.Status = domain.StatusActive
    }
    // upsert
    q := `INSERT INTO instances (id, hostname, ip, meta, status, last_seen)
        VALUES ($1,$2,$3,$4,$5,$6)
        ON CONFLICT (id) DO UPDATE SET hostname = EXCLUDED.hostname, ip = EXCLUDED.ip, meta = EXCLUDED.meta, status = EXCLUDED.status, last_seen = EXCLUDED.last_seen`
    _, err := r.db.Exec(q, instance.ID, instance.Hostname, instance.IP, meta, string(instance.Status), instance.LastSeen)
    return err
}

func (r *PostgresRegistry) GetByID(id string) (*domain.Instance, error) {
    q := `SELECT id, hostname, ip, meta, status, last_seen FROM instances WHERE id=$1`
    row := r.db.QueryRow(q, id)
    var inst domain.Instance
    var metaBytes []byte
    if err := row.Scan(&inst.ID, &inst.Hostname, &inst.IP, &metaBytes, &inst.Status, &inst.LastSeen); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, fmt.Errorf("not found")
        }
        return nil, err
    }
    if len(metaBytes) > 0 {
        _ = json.Unmarshal(metaBytes, &inst.Meta)
    }
    return &inst, nil
}

func (r *PostgresRegistry) List() ([]*domain.Instance, error) {
    q := `SELECT id, hostname, ip, meta, status, last_seen FROM instances`
    rows, err := r.db.Query(q)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var res []*domain.Instance
    for rows.Next() {
        var inst domain.Instance
        var metaBytes []byte
        if err := rows.Scan(&inst.ID, &inst.Hostname, &inst.IP, &metaBytes, &inst.Status, &inst.LastSeen); err != nil {
            return nil, err
        }
        if len(metaBytes) > 0 {
            _ = json.Unmarshal(metaBytes, &inst.Meta)
        }
        res = append(res, &inst)
    }
    return res, nil
}

func (r *PostgresRegistry) MarkInactive(id string) error {
    q := `UPDATE instances SET status=$1 WHERE id=$2`
    res, err := r.db.Exec(q, string(domain.StatusInactive), id)
    if err != nil {
        return err
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        return fmt.Errorf("not found")
    }
    return nil
}
