package httpadapter

import (
    "encoding/json"
    "log"
    "net/http"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
)

type Handler struct {
    svc      *service.MonitorService
    eventSvc *service.EventService
}

func NewHandler(svc *service.MonitorService) *Handler {
    return &Handler{svc: svc}
}

// HU-29: constructor para handler con solo eventSvc (usado por tests)
func NewHandlerEventOnly(eventSvc *service.EventService) *Handler {
    return &Handler{eventSvc: eventSvc}
}

func NewHandlerWithEventService(svc *service.MonitorService, eventSvc *service.EventService) *Handler {
    return &Handler{svc: svc, eventSvc: eventSvc}
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

// HU-29: GET /events — lista todos los eventos, acepta ?severity=CRITICAL&type=failure
func (h *Handler) GetEvents(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        w.WriteHeader(http.StatusMethodNotAllowed)
        return
    }
    if h.eventSvc == nil {
        http.Error(w, "event service not configured", http.StatusInternalServerError)
        return
    }
    filter := map[string]string{}
    if sev := r.URL.Query().Get("severity"); sev != "" {
        filter["severity"] = sev
    }
    if t := r.URL.Query().Get("type"); t != "" {
        filter["type"] = t
    }
    events, err := h.eventSvc.GetEvents(filter)
    if err != nil {
        log.Printf("GetEvents error: %v", err)
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }
    if events == nil {
        events = []*domain.SystemEvent{}
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(events)
}

// HU-29: GET /events/critical — atajo para eventos CRITICAL unicamente
func (h *Handler) GetCriticalEvents(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        w.WriteHeader(http.StatusMethodNotAllowed)
        return
    }
    if h.eventSvc == nil {
        http.Error(w, "event service not configured", http.StatusInternalServerError)
        return
    }
    events, err := h.eventSvc.GetCriticalEvents()
    if err != nil {
        log.Printf("GetCriticalEvents error: %v", err)
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }
    if events == nil {
        events = []*domain.SystemEvent{}
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(events)
}
