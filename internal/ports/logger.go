// internal/ports/logger.go
// HU-11: puerto (interfaz) del sistema de logs
package ports

import "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"

type EventLogger interface {
    Log(event *domain.SystemEvent) error
    List(filter map[string]string) ([]*domain.SystemEvent, error)
}
