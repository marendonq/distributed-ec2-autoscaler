package domain

type InstanceStatus string

const (
    StatusActive   InstanceStatus = "active"
    StatusInactive InstanceStatus = "inactive"
)

type Instance struct {
    ID       string            `json:"id"`
    Hostname string            `json:"hostname,omitempty"`
    IP       string            `json:"ip,omitempty"`
    Meta     map[string]string `json:"meta,omitempty"`
    Status   InstanceStatus    `json:"status"`
    LastSeen int64             `json:"last_seen"`
}
