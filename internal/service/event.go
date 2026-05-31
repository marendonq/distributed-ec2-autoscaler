// internal/service/event.go
// HU-11: servicio de aplicacion para eventos del sistema
package service

import (
    "log"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
)

type EventService struct {
    logger ports.EventLogger
}

func NewEventService(logger ports.EventLogger) *EventService {
    return &EventService{logger: logger}
}

// RecordEvent persiste el evento. Si el logger es nil o falla,
// registra en stderr como fallback — nunca propaga el error.
func (s *EventService) RecordEvent(evt *domain.SystemEvent) {
    if s == nil || s.logger == nil {
        return
    }
    // HU-29: log fallback incluye severidad
    if err := s.logger.Log(evt); err != nil {
        log.Printf("[HU-29] event log fallback (type=%s severity=%s): %v", evt.Type, evt.Severity, err)
    }
}

// HU-29: GetCriticalEvents retorna todos los eventos con severidad CRITICAL.
func (s *EventService) GetCriticalEvents() ([]*domain.SystemEvent, error) {
    return s.GetEvents(map[string]string{"severity": string(domain.SeverityCritical)})
}

func (s *EventService) GetEvents(filter map[string]string) ([]*domain.SystemEvent, error) {
    if s == nil || s.logger == nil {
        return nil, nil
    }
    return s.logger.List(filter)
}
