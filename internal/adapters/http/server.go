package httpadapter

import (
    "context"
    "log"
    "net/http"
    "time"

    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
)

func StartHTTPServer(ctx context.Context, svc *service.MonitorService, addr string) error {
    h := NewHandler(svc)
    mux := http.NewServeMux()
    mux.HandleFunc("/register", h.RegisterEndpoint)
    mux.HandleFunc("/heartbeat", h.HeartbeatEndpoint)
    mux.HandleFunc("/instances", h.ListEndpoint)

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

    log.Printf("HTTP server listening on %s", addr)
    return srv.ListenAndServe()
}
