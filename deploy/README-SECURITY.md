# Restricción de Acceso entre Servicios

## Descripción

El sistema aplica el principio de mínimo privilegio de red mediante dos Security Groups independientes. Cada tipo de instancia tiene su propio SG con reglas que permiten exclusivamente el tráfico necesario para su funcionamiento. Todo lo demás está bloqueado por defecto.

Las AppInstances están en una subnet privada sin acceso a Internet y sin IP pública. La instancia de control actúa como bastion host para acceso SSH a las AppInstances.

## Security Groups

### SG-Control

Asignado a la instancia donde corren MonitorS y ControllerASG (subnet pública).

**Inbound:**

| Tipo | Protocolo | Puerto | Fuente | Propósito |
|------|-----------|--------|--------|-----------|
| SSH | TCP | 22 | `<TU_IP_PUBLICA>/32` | Acceso administrativo por SSH |
| Custom TCP | TCP | 50052 | `172.20.2.0/24` | gRPC Register/Deregister desde MonitorC |

**Outbound:** todo permitido (default). Necesario para que el ControllerASG se comunique con el SDK de AWS por HTTPS y para que el MonitorS haga polling a las AppInstances.

### SG-AppInstance

Asignado a cada instancia creada por el ControllerASG (subnet privada).

**Inbound:**

| Tipo | Protocolo | Puerto | Fuente | Propósito |
|------|-----------|--------|--------|-----------|
| SSH | TCP | 22 | SG-Control | Debug vía bastion host |
| Custom TCP | TCP | 50051 | SG-Control | gRPC Ping/GetMetrics/Shutdown desde MonitorS |

**Outbound:** todo permitido (default). En la práctica las AppInstances no generan tráfico saliente porque no tienen acceso a Internet (subnet privada sin NAT Gateway).

## Por qué se usa referencia por Security Group

En las reglas de SG-AppInstance, la fuente no es una IP ni un rango CIDR, sino el SG-Control. Esto significa que cualquier instancia que tenga asignado SG-Control puede comunicarse con cualquier instancia que tenga SG-AppInstance, sin importar sus IPs.

Esto es crítico para el auto-escalamiento: cuando el ControllerASG crea una nueva instancia y le asigna SG-AppInstance, las reglas de firewall aplican automáticamente. Si se usaran IPs fijas, cada instancia nueva requeriría una actualización manual de las reglas del Security Group, lo cual haría imposible el escalamiento automático.

## Procedimiento de Creación

### Paso 1: Crear SG-Control

1. En la consola de AWS, ir a VPC > Security Groups > Create Security Group.
2. Nombre: `SG-Control`.
3. Descripción: `Security group for MonitorS and ControllerASG`.
4. VPC: seleccionar la VPC del proyecto (`172.20.0.0/16`).
5. Agregar reglas inbound según la tabla de arriba.
6. Click en Create Security Group.
7. Anotar el SG ID (ej. `sg-0abc123`).

### Paso 2: Crear SG-AppInstance

1. En la consola de AWS, ir a VPC > Security Groups > Create Security Group.
2. Nombre: `SG-AppInstance`.
3. Descripción: `Security group for auto-scaled AppInstances`.
4. VPC: seleccionar la VPC del proyecto.
5. Agregar reglas inbound según la tabla de arriba. En la columna Source, buscar y seleccionar `SG-Control` por nombre (no escribir un ID manualmente).
6. Click en Create Security Group.
7. Anotar el SG ID (ej. `sg-0def456`).

### Paso 3: Actualizar config.json

Poner el SG ID de SG-AppInstance en el campo `security_groups`:

```json
"security_groups": ["sg-0def456"]
```

Este es el SG que el ControllerASG asigna automáticamente a cada instancia que crea.

### Paso 4: Asignar SG-Control a la instancia de control

Al lanzar (o modificar) la instancia donde corren MonitorS y ControllerASG, asignarle SG-Control.

## Diagrama de Tráfico

```
Tu máquina ──SSH:22──> [SG-Control] Instancia de Control (MonitorS + ControllerASG)
                              │
                              ├──gRPC:50051──> [SG-AppInstance] AppInstance 1 (MonitorC + FastAPI)
                              ├──gRPC:50051──> [SG-AppInstance] AppInstance 2 (MonitorC + FastAPI)
                              └──gRPC:50051──> [SG-AppInstance] AppInstance N (MonitorC + FastAPI)
                              
                              ▲
                              │
               gRPC:50052 Register/Deregister
                              │
                    [SG-AppInstance] AppInstances
```