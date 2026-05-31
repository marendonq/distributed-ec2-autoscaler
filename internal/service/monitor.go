package service

import (
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
)

type MonitorService struct {
    registry ports.InstanceRegistry
    EventSvc *EventService
}

func NewMonitorService(r ports.InstanceRegistry, eventSvc *EventService) *MonitorService {
    return &MonitorService{registry: r, EventSvc: eventSvc}
}

func (s *MonitorService) RegisterInstance(inst *domain.Instance) error {
    return s.registry.Register(inst)
}

func (s *MonitorService) ListInstances() ([]*domain.Instance, error) {
    return s.registry.List()
}

func (s *MonitorService) MarkInactive(id string) error {
    return s.registry.MarkInactive(id)
}

func (s *MonitorService) Deregister(id string) error {
    return s.registry.Delete(id)
}

// Heartbeat updates last-seen status for an instance. If instance does not
// exist it will create a lightweight registration with the provided ID.
func (s *MonitorService) Heartbeat(id string) error {
    inst := &domain.Instance{ID: id}
    return s.registry.Register(inst)
}

func (s *MonitorService) GetEvents(filter map[string]string) ([]*domain.SystemEvent, error) {
    if s == nil || s.EventSvc == nil {
        return nil, nil
    }
    return s.EventSvc.GetEvents(filter)
}

func (s *MonitorService) RecordMetric(instanceID string, load float32, timestamp int64) error {
    return s.registry.RecordMetric(instanceID, load, timestamp)
}
