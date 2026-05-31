// internal/domain/log.go
// HU-11: entidad de dominio para eventos del sistema
package domain

import "time"

type EventType string

const (
    EventInstanceCreated        EventType = "instance_created"
    EventInstanceDeleted        EventType = "instance_deleted"
    EventInstanceMarkedInactive EventType = "instance_marked_inactive"
    EventMonitorCRegistered     EventType = "monitor_c_registered"
    EventMetricsRecorded        EventType = "metrics_recorded"
    EventFailure                EventType = "failure"
    EventASGCooldownActive      EventType = "asg_cooldown_active"
)

type SystemEvent struct {
    ID        string            `json:"id"`
    Type      EventType         `json:"type"`
    Message   string            `json:"message"`
    Metadata  map[string]string `json:"metadata,omitempty"`
    Timestamp int64             `json:"timestamp"`
}

// NewSystemEvent construye un evento con Timestamp auto-generado.
// El ID sera asignado por el adaptador al persistir.
func NewSystemEvent(t EventType, msg string, meta map[string]string) *SystemEvent {
    return &SystemEvent{
        Type:      t,
        Message:   msg,
        Metadata:  meta,
        Timestamp: time.Now().Unix(),
    }
}
