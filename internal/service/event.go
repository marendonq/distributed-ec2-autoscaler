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
    if err := s.logger.Log(evt); err != nil {
        log.Printf("[HU-11] event log fallback (type=%s): %v", evt.Type, err)
    }
}

func (s *EventService) GetEvents(filter map[string]string) ([]*domain.SystemEvent, error) {
    if s == nil || s.logger == nil {
        return nil, nil
    }
    return s.logger.List(filter)
}
