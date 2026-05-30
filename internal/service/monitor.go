package service

import (
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
)

type MonitorService struct {
    registry ports.InstanceRegistry
}

func NewMonitorService(r ports.InstanceRegistry) *MonitorService {
    return &MonitorService{registry: r}
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
