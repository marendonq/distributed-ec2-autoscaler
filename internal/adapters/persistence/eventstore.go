// internal/adapters/persistence/eventstore.go
// HU-11: adaptador SQLite para el sistema de logs
package persistence

import (
    "database/sql"
    "encoding/json"
    "strings"
    "time"

    "github.com/google/uuid"
    _ "modernc.org/sqlite"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
)

type SQLiteEventStore struct {
    db *sql.DB
}

func NewSQLiteEventStore(dsn string) (ports.EventLogger, error) {
    db, err := sql.Open("sqlite", dsn)
    if err != nil { return nil, err }
    if err := db.Ping(); err != nil { db.Close(); return nil, err }
    s := &SQLiteEventStore{db: db}
    if err := s.ensureSchema(); err != nil { db.Close(); return nil, err }
    return s, nil
}

func (s *SQLiteEventStore) ensureSchema() error {
    // HU-11: crear tabla events si no existe
    _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS events (
        id        TEXT PRIMARY KEY,
        type      TEXT NOT NULL,
        message   TEXT NOT NULL,
        metadata  TEXT,
        timestamp BIGINT NOT NULL
    )`)
    return err
}

func (s *SQLiteEventStore) Log(event *domain.SystemEvent) error {
    if event.ID == "" {
        event.ID = uuid.New().String()
    }
    if event.Timestamp == 0 {
        event.Timestamp = time.Now().Unix()
    }
    meta := []byte("null")
    if event.Metadata != nil {
        b, err := json.Marshal(event.Metadata)
        if err != nil { return err }
        meta = b
    }
    _, err := s.db.Exec(
        `INSERT INTO events (id, type, message, metadata, timestamp) VALUES (?,?,?,?,?)`,
        event.ID, string(event.Type), event.Message, string(meta), event.Timestamp,
    )
    return err
}

func (s *SQLiteEventStore) List(filter map[string]string) ([]*domain.SystemEvent, error) {
    q := `SELECT id, type, message, metadata, timestamp FROM events`
    var args []interface{}
    var where []string
    if t, ok := filter["type"]; ok {
        where = append(where, "type = ?")
        args = append(args, t)
    }
    if after, ok := filter["after_timestamp"]; ok {
        where = append(where, "timestamp > ?")
        args = append(args, after)
    }
    if len(where) > 0 {
        q += " WHERE " + strings.Join(where, " AND ")
    }
    q += " ORDER BY timestamp DESC"

    rows, err := s.db.Query(q, args...)
    if err != nil { return nil, err }
    defer rows.Close()

    var res []*domain.SystemEvent
    for rows.Next() {
        var e domain.SystemEvent
        var metaStr string
        if err := rows.Scan(&e.ID, &e.Type, &e.Message, &metaStr, &e.Timestamp); err != nil {
            return nil, err
        }
        if metaStr != "" && metaStr != "null" {
            _ = json.Unmarshal([]byte(metaStr), &e.Metadata)
        }
        res = append(res, &e)
    }
    return res, rows.Err()
}
