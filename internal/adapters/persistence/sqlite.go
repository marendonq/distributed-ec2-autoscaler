package persistence

import (
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "strconv"
    "sync"
    "time"

    _ "modernc.org/sqlite"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
)

type SQLiteRegistry struct {
    db *sql.DB
    mu sync.RWMutex
}

// NewSQLiteRegistry opens a sqlite connection with provided DSN (e.g.
// file:teleproy2.db?cache=shared&mode=rwc) and ensures the instances table exists.
func NewSQLiteRegistry(dsn string) (ports.InstanceRegistry, error) {
    db, err := sql.Open("sqlite", dsn)
    if err != nil {
        return nil, err
    }
    if err := db.Ping(); err != nil {
        return nil, err
    }
    r := &SQLiteRegistry{db: db}
    if err := r.ensureSchema(); err != nil {
        db.Close()
        return nil, err
    }
    return r, nil
}

func (r *SQLiteRegistry) ensureSchema() error {
    q := `CREATE TABLE IF NOT EXISTS instances (
        id TEXT PRIMARY KEY,
        hostname TEXT,
        ip TEXT,
        meta TEXT,
        status TEXT,
        last_seen BIGINT
    )`
    _, err := r.db.Exec(q)
    return err
}

func (r *SQLiteRegistry) Register(instance *domain.Instance) error {
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
    // upsert in sqlite
    q := `INSERT INTO instances (id, hostname, ip, meta, status, last_seen)
        VALUES (?,?,?,?,?,?)
        ON CONFLICT (id) DO UPDATE SET hostname = excluded.hostname, ip = excluded.ip, meta = excluded.meta, status = excluded.status, last_seen = excluded.last_seen`
    _, err := r.db.Exec(q, instance.ID, instance.Hostname, instance.IP, string(meta), string(instance.Status), instance.LastSeen)
    return err
}

func (r *SQLiteRegistry) GetByID(id string) (*domain.Instance, error) {
    q := `SELECT id, hostname, ip, meta, status, last_seen FROM instances WHERE id=?`
    row := r.db.QueryRow(q, id)
    var inst domain.Instance
    var metaString string
    if err := row.Scan(&inst.ID, &inst.Hostname, &inst.IP, &metaString, &inst.Status, &inst.LastSeen); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, fmt.Errorf("not found")
        }
        return nil, err
    }
    if metaString != "" && metaString != "null" {
        _ = json.Unmarshal([]byte(metaString), &inst.Meta)
    }
    return &inst, nil
}

func (r *SQLiteRegistry) List() ([]*domain.Instance, error) {
    q := `SELECT id, hostname, ip, meta, status, last_seen FROM instances`
    rows, err := r.db.Query(q)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var res []*domain.Instance
    for rows.Next() {
        var inst domain.Instance
        var metaString string
        if err := rows.Scan(&inst.ID, &inst.Hostname, &inst.IP, &metaString, &inst.Status, &inst.LastSeen); err != nil {
            return nil, err
        }
        if metaString != "" && metaString != "null" {
            _ = json.Unmarshal([]byte(metaString), &inst.Meta)
        }
        res = append(res, &inst)
    }
    return res, nil
}

func (r *SQLiteRegistry) MarkInactive(id string) error {
    q := `UPDATE instances SET status=? WHERE id=?`
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

func (r *SQLiteRegistry) Delete(id string) error {
    q := `DELETE FROM instances WHERE id=?`
    res, err := r.db.Exec(q, id)
    if err != nil {
        return err
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        return fmt.Errorf("not found")
    }
    return nil
}

func (r *SQLiteRegistry) GetAggregatedMetrics() (float32, int, int, error) {
    var totalLoad float32 = 0
    activeCount := 0
    inactiveCount := 0

    r.mu.RLock()
    defer r.mu.RUnlock()

    q := `SELECT meta, status FROM instances`
    rows, err := r.db.Query(q)
    if err != nil {
        return 0, 0, 0, err
    }
    defer rows.Close()

    for rows.Next() {
        var metaStr, status string
        if err := rows.Scan(&metaStr, &status); err != nil {
            return 0, 0, 0, err
        }

        if status == string(domain.StatusActive) {
            activeCount++
            if metaStr != "" && metaStr != "null" {
                var meta map[string]string
                if err := json.Unmarshal([]byte(metaStr), &meta); err == nil {
                    if loadStr, ok := meta["cpu_load"]; ok {
                        if load, err := strconv.ParseFloat(loadStr, 32); err == nil {
                            totalLoad += float32(load)
                        }
                    }
                }
            }
        } else {
            inactiveCount++
        }
    }

    avgLoad := float32(0)
    if activeCount > 0 {
        avgLoad = totalLoad / float32(activeCount)
    }

    return avgLoad, activeCount, inactiveCount, nil
}
