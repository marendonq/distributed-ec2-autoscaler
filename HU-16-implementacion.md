# HU-16: Reconexión Automática de MonitorC

## Contexto

Proyecto Go con arquitectura hexagonal. MonitorC es un agente que corre en cada instancia EC2, se registra ante MonitorS vía gRPC al inicio, y expone `Ping`/`GetMetrics` para polling. El problema actual: si MonitorS cae o hay fallo de red, MonitorC no detecta la desconexión ni reintenta el registro. Requiere intervención manual.

Dependencias ya implementadas que se reutilizan:
- `internal/domain/log.go` — `SystemEvent`, `EventType`, `Severity` (HU-29)
- Variables de entorno `MONITOR_S_ADDR`, `MONITOR_C_LISTEN_ADDR`, `INSTANCE_ID` ya soportadas (HU-34)

---

## Tarea

Implementar reconexión automática con backoff exponencial en MonitorC. El servidor gRPC local (que sirve `Ping`/`GetMetrics`) debe seguir corriendo durante la reconexión. **No romper el comportamiento existente del lado de MonitorS.**

---

## Pasos obligatorios

### Paso 1 — Leer archivos clave antes de escribir código

Leer en su totalidad:

- `cmd/monitorc/main.go` — flujo actual completo, incluyendo el loop de registro (líneas ~165-176), flags, y el servidor gRPC
- `pkg/monitor/monitor.go` — servidor MonitorS: método `Register`, `pollInstance`, configuración del servidor gRPC
- `internal/domain/log.go` — constantes `EventType` y `Severity` existentes
- `internal/adapters/cloud/ec2.go` — función `GetInstanceMetadata` si existe

El objetivo es conocer: la firma exacta del RPC `Register`, el tipo `RegisterRequest`, el tipo `RegisterResponse`, el paquete protobuf importado, y cómo `getEnvOrDefault` ya está definida (si lo está).

---

### Paso 2 — Añadir constantes de evento en `internal/domain/log.go`

Añadir únicamente las constantes que no existan ya:

```go
EventMonitorCConnectionLost     EventType = "monitor_c_connection_lost"
EventMonitorCReconnected        EventType = "monitor_c_reconnected"
EventMonitorCRegistrationFailed EventType = "monitor_c_registration_failed"
```

Verificar antes de añadir que no están duplicadas. No modificar ninguna otra parte del archivo.

---

### Paso 3 — Definir `MonitorCConfig` y `loadMonitorCConfig` en `cmd/monitorc/main.go`

Añadir el struct de configuración estructurada:

```go
type MonitorCConfig struct {
    MonitorSAddr string
    ListenAddr   string
    InstanceID   string
    Hostname     string
    LocalIP      string
    ListenPort   int
    Environment  string
}
```

Añadir la función `loadMonitorCConfig() (*MonitorCConfig, error)` que:

1. Obtiene `hostname` con `os.Hostname()`, fallback `"unknown"`.
2. Obtiene `localIP` con la función existente `getLocalIP()` (verificar su nombre real en el paso 1).
3. Obtiene `instanceID` desde `INSTANCE_ID` env var. Si está vacía, intentar `cloud.GetInstanceMetadata()` si existe esa función. Si sigue vacía, usar `hostname`.
4. Obtiene `listenAddr` desde `getEnvOrDefault("MONITOR_C_LISTEN_ADDR", ":50052")`.
5. Extrae el puerto numérico con `net.SplitHostPort`. Si falla o el puerto es 0, usar `50052`.
6. Retorna el struct poblado. El campo `Environment` toma `getEnvOrDefault("ENV", "production")`.

Si `getEnvOrDefault` ya está definida en el archivo (por HU-34), no redefinirla.

---

### Paso 4 — Implementar `connectAndRegister`

Añadir la función:

```go
func connectAndRegister(ctx context.Context, cfg *MonitorCConfig) (*grpc.ClientConn, pb.MonitorSServiceClient, error)
```

Lógica:

1. Crear contexto con timeout de 5s para el dial: `context.WithTimeout(ctx, 5*time.Second)`.
2. Llamar a `grpc.DialContext` con `grpc.WithTransportCredentials(insecure.NewCredentials())` y `grpc.WithBlock()`. Cancelar el contexto de dial al terminar.
3. Si hay error, retornar `nil, nil, fmt.Errorf("failed to connect to MonitorS: %w", err)`.
4. Crear el client: `pb.NewMonitorSServiceClient(conn)`.
5. Construir `RegisterRequest` con los campos del `cfg` (verificar nombres exactos del proto en el paso 1).
6. Crear contexto con timeout de 5s para el registro.
7. Llamar a `client.Register`. Si hay error: cerrar `conn`, retornar error con `EventMonitorCRegistrationFailed` implícito (el caller lo registrará).
8. Si `resp.Success == false`: cerrar `conn`, retornar `fmt.Errorf("registration rejected: %s", resp.Message)`.
9. Retornar `conn, client, nil`.

Añadir keepalive al dial:

```go
grpc.WithKeepaliveParams(keepalive.ClientParameters{
    Time:                10 * time.Second,
    Timeout:             3 * time.Second,
    PermitWithoutStream: true,
})
```

Importar `"google.golang.org/grpc/keepalive"`.

---

### Paso 5 — Implementar `monitorConnection`

Añadir la función:

```go
func monitorConnection(ctx context.Context, conn *grpc.ClientConn, client pb.MonitorSServiceClient, cfg *MonitorCConfig) 
```

Lógica con ticker de 10 segundos:

1. Crear `time.NewTicker(10 * time.Second)`, defer `ticker.Stop()`.
2. En cada tick:
   - Verificar `conn.GetState()`. Si el estado no es `connectivity.Idle` ni `connectivity.Ready`: loggear y retornar (señal a `runReconnector` de que debe reconectar).
   - Hacer un Ping con timeout de 2s: `client.Ping(ctxPing, &pb.PingRequest{})`. Si hay error: loggear y retornar.
3. Si `ctx.Done()` se cierra: retornar.

Importar `"google.golang.org/grpc/connectivity"`.

---

### Paso 6 — Implementar `runReconnector`

Añadir la función:

```go
func runReconnector(ctx context.Context, cfg *MonitorCConfig)
```

Constantes locales al archivo o al paquete:

```go
const (
    initialBackoff = 2 * time.Second
    maxBackoff     = 30 * time.Second
)
```

Lógica del loop:

1. Iniciar `backoff := initialBackoff` y `retries := 0`.
2. Loop `for`:
   - Verificar `ctx.Done()` al inicio de cada iteración; si está cancelado, retornar.
   - Llamar `connectAndRegister(ctx, cfg)`.
   - **Si hay error**: loggear el error. Esperar `backoff` con `select { case <-ctx.Done(): return; case <-time.After(backoff) }`. Aumentar backoff: `backoff = min(time.Duration(float64(backoff)*1.5), maxBackoff)`. Incrementar `retries`. Continuar.
   - **Si éxito**: loggear reconexión exitosa. Reset `backoff = initialBackoff`, `retries = 0`. Llamar `monitorConnection(ctx, conn, client, cfg)` (bloqueante). Cerrar `conn` con `conn.Close()`. Si `ctx.Err() != nil`, retornar. Si no, continuar el loop (reconectar).

---

### Paso 7 — Reestructurar `main` en `cmd/monitorc/main.go`

Reemplazar el `main` actual manteniendo el comportamiento del servidor gRPC local. El nuevo flujo:

1. Crear contexto cancelable con señales del OS: usar `signal.NotifyContext` o el mecanismo que ya exista en el archivo.
2. Llamar `loadMonitorCConfig()`. Si falla, `log.Fatalf`.
3. Iniciar listener TCP con `net.Listen("tcp", cfg.ListenAddr)`.
4. Crear servidor gRPC local, registrar la implementación de `MonitorCService` existente.
5. Lanzar el servidor en goroutine: `go srv.Serve(lis)`.
6. Lanzar el reconector en goroutine: `go runReconnector(ctx, cfg)`.
7. Bloquear con `<-ctx.Done()`.
8. Llamar `srv.GracefulStop()`.

**Eliminar** el loop de reintento de registro inicial existente (líneas ~165-176). `runReconnector` lo reemplaza por completo.

No eliminar ni modificar la implementación del servidor gRPC local (`monitorCServer`, `Ping`, `GetMetrics`).

---

### Paso 8 — Añadir keepalive en MonitorS

**Archivo**: `pkg/monitor/monitor.go`

Localizar donde se crea `grpc.NewServer()` para MonitorS. Añadir parámetros de keepalive del lado servidor:

```go
grpc.KeepaliveParams(keepalive.ServerParameters{
    Time:    30 * time.Second,
    Timeout: 5 * time.Second,
})
```

Importar `"google.golang.org/grpc/keepalive"`. No cambiar ninguna otra lógica del servidor.

---

### Paso 9 — Verificar compilación

```bash
go build ./...
```

Corregir errores antes de continuar. Verificar en especial:
- Imports de `connectivity` y `keepalive` correctamente referenciados.
- Que `pb.PingRequest{}` existe en el proto (si no existe, eliminar el ping de `monitorConnection` y dejar solo la verificación de estado).
- Que los campos de `RegisterRequest` coinciden con la definición proto real.

---

### Paso 10 — Implementar tests

**Test `connectAndRegister` con fallo de dial**: usar variable de función `dialFn` reemplazable, o simplemente pasar una dirección inválida y verificar que retorna error sin panic.

**Test `connectAndRegister` con éxito**: requiere un servidor gRPC de prueba local. Usar `bufconn` o un servidor real en un puerto efímero. Verificar que retorna `conn` y `client` no nulos.

**Test de backoff**: crear una versión de `runReconnector` con `dialFn` mockeable. Simular 3 fallos consecutivos. Verificar que el intervalo entre intentos crece (2s → ~3s → ~4.5s) y no supera `maxBackoff`.

**Test de shutdown limpio**: iniciar `runReconnector` en goroutine con context cancelable. Cancelar el context mientras está esperando en backoff. Verificar que la goroutine termina en menos de `maxBackoff + 1s` (usar `time.After` con margen).

Ejecutar:

```bash
go test ./cmd/monitorc/...
```

---

## Restricciones

- El servidor gRPC local de MonitorC (que sirve `Ping`/`GetMetrics`) debe seguir corriendo durante toda la reconexión. No detenerlo ni reiniciarlo.
- No modificar el método `Register` en MonitorS. Solo se añaden keepalive params al servidor.
- No cambiar las interfaces de dominio ni los puertos existentes.
- No añadir dependencias externas (solo `google.golang.org/grpc` y stdlib que ya están en el proyecto).
- `runReconnector` debe terminar limpiamente cuando el context se cancela, sin goroutines filtradas.
- Si `client.Ping` no existe en el proto, omitir el ping en `monitorConnection` y usar solo `conn.GetState()`.

---

## Verificación final

- [ ] Constantes `EventMonitorCConnectionLost`, `EventMonitorCReconnected`, `EventMonitorCRegistrationFailed` en `domain/log.go`
- [ ] `MonitorCConfig` y `loadMonitorCConfig` implementados
- [ ] `connectAndRegister` implementado con keepalive y timeout de 5s por operación
- [ ] `monitorConnection` implementado con ticker de 10s y verificación de estado gRPC
- [ ] `runReconnector` implementado con backoff exponencial (`initialBackoff=2s`, `maxBackoff=30s`, factor 1.5)
- [ ] Loop de registro inicial eliminado de `main`; reemplazado por `runReconnector`
- [ ] Servidor gRPC local sigue corriendo independientemente del reconector
- [ ] Keepalive añadido en MonitorS (`pkg/monitor/monitor.go`)
- [ ] `go build ./...` pasa sin errores
- [ ] Tests de `connectAndRegister`, backoff y shutdown limpio pasan