package ports

import "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"

type InstanceRegistry interface {
    Register(instance *domain.Instance) error
    GetByID(id string) (*domain.Instance, error)
    List() ([]*domain.Instance, error)
    MarkInactive(id string) error
    Delete(id string) error
}
