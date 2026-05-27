package httpadapter

import (
    "encoding/json"
    "log"
    "net/http"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
)

type Handler struct {
    svc *service.MonitorService
}

func NewHandler(svc *service.MonitorService) *Handler {
    return &Handler{svc: svc}
}

func (h *Handler) RegisterEndpoint(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        w.WriteHeader(http.StatusMethodNotAllowed)
        return
    }
    var inst domain.Instance
    if err := json.NewDecoder(r.Body).Decode(&inst); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest)
        return
    }
    if inst.ID == "" {
        http.Error(w, "id required", http.StatusBadRequest)
        return
    }
    if err := h.svc.RegisterInstance(&inst); err != nil {
        log.Printf("register error: %v", err)
        http.Error(w, "failed to register", http.StatusInternalServerError)
        return
    }
    w.WriteHeader(http.StatusCreated)
    _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) ListEndpoint(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        w.WriteHeader(http.StatusMethodNotAllowed)
        return
    }
    items, err := h.svc.ListInstances()
    if err != nil {
        http.Error(w, "failed", http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(items)
}

func (h *Handler) HeartbeatEndpoint(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        w.WriteHeader(http.StatusMethodNotAllowed)
        return
    }
    var payload struct{
        ID string `json:"id"`
    }
    if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest)
        return
    }
    if payload.ID == "" {
        http.Error(w, "id required", http.StatusBadRequest)
        return
    }
    if err := h.svc.Heartbeat(payload.ID); err != nil {
        log.Printf("heartbeat error: %v", err)
        http.Error(w, "failed", http.StatusInternalServerError)
        return
    }
    w.WriteHeader(http.StatusOK)
    _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
