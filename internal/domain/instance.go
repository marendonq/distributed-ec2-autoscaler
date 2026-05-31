package domain

import "strconv"

type InstanceStatus string

const (
    StatusActive   InstanceStatus = "active"
    StatusInactive InstanceStatus = "inactive"
)

type Instance struct {
    ID        string            `json:"id"`
    Hostname  string            `json:"hostname,omitempty"`
    IP        string            `json:"ip,omitempty"`
    Meta      map[string]string `json:"meta,omitempty"`
    Status    InstanceStatus    `json:"status"`
    LastSeen  int64             `json:"last_seen"`
    CreatedAt int64             `json:"created_at,omitempty"`
}

func (i *Instance) CreatedAtUnix() int64 {
    if i.CreatedAt > 0 {
        return i.CreatedAt
    }
    if i.Meta != nil {
        if v, ok := i.Meta["created_at"]; ok && v != "" {
            if n, err := strconv.ParseInt(v, 10, 64); err == nil {
                return n
            }
        }
    }
    return i.LastSeen
}

func (i *Instance) ExcludedFromAvg() bool {
    return i.Meta != nil && i.Meta["excluded_from_avg"] == "true"
}
