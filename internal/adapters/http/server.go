package httpadapter

import (
    "context"
    "log"
    "net/http"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
)

func StartHTTPServer(ctx context.Context, svc *service.MonitorService, addr string) error {
    return StartHTTPServerWithEventService(ctx, svc, nil, addr)
}

// HU-29: version con soporte para eventSvc
func StartHTTPServerWithEventService(ctx context.Context, svc *service.MonitorService, eventSvc *service.EventService, addr string) error {
    h := NewHandlerWithEventService(svc, eventSvc)
    mux := http.NewServeMux()
    mux.HandleFunc("/register", h.RegisterEndpoint)
    mux.HandleFunc("/heartbeat", h.HeartbeatEndpoint)
    mux.HandleFunc("/instances", h.ListEndpoint)
    // HU-29: endpoints para eventos
    mux.HandleFunc("/events", h.GetEvents)
    mux.HandleFunc("/events/critical", h.GetCriticalEvents)

    srv := &http.Server{
        Addr:    addr,
        Handler: mux,
    }

    go func() {
        <-ctx.Done()
        log.Println("shutting down HTTP server")
        ctxShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        srv.Shutdown(ctxShutdown)
    }()

    log.Printf("[HU-29] HTTP server listening on %s", addr)
    return srv.ListenAndServe()
}
