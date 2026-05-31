package persistence

import (
    "errors"
    "strconv"
    "sync"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/ports"
)

type InMemoryRegistry struct {
    mu    sync.RWMutex
    store map[string]*domain.Instance
}

func NewInMemoryRegistry() ports.InstanceRegistry {
    return &InMemoryRegistry{store: make(map[string]*domain.Instance)}
}

func (r *InMemoryRegistry) Register(instance *domain.Instance) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    now := time.Now().Unix()
    if existing, ok := r.store[instance.ID]; ok {
        existing.LastSeen = now
        existing.Status = domain.StatusActive
        if instance.Hostname != "" {
            existing.Hostname = instance.Hostname
        }
        if instance.IP != "" {
            existing.IP = instance.IP
        }
        if instance.Meta != nil {
            existing.Meta = instance.Meta
        }
        return nil
    }
    instance.LastSeen = now
    instance.Status = domain.StatusActive
    r.store[instance.ID] = instance
    return nil
}

func (r *InMemoryRegistry) GetByID(id string) (*domain.Instance, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    inst, ok := r.store[id]
    if !ok {
        return nil, errors.New("not found")
    }
    return inst, nil
}

func (r *InMemoryRegistry) List() ([]*domain.Instance, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    res := make([]*domain.Instance, 0, len(r.store))
    for _, v := range r.store {
        res = append(res, v)
    }
    return res, nil
}

func (r *InMemoryRegistry) MarkInactive(id string) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    inst, ok := r.store[id]
    if !ok {
        return errors.New("not found")
    }
    inst.Status = domain.StatusInactive
    return nil
}

func (r *InMemoryRegistry) Delete(id string) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    if _, ok := r.store[id]; !ok {
        return errors.New("not found")
    }
    delete(r.store, id)
    return nil
}

func (r *InMemoryRegistry) GetAggregatedMetrics() (float32, int, int, error) {
    var totalLoad float32 = 0
    activeCount := 0
    inactiveCount := 0

    r.mu.RLock()
    defer r.mu.RUnlock()

    for _, inst := range r.store {
        if inst.Status == domain.StatusActive {
            activeCount++
            if loadStr, ok := inst.Meta["cpu_load"]; ok {
                if load, err := strconv.ParseFloat(loadStr, 32); err == nil {
                    totalLoad += float32(load)
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
