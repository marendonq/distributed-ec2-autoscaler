# Configuración Centralizada del Sistema

## Descripción

Toda la configuración del sistema de auto-escalamiento se gestiona desde un único archivo: `config.json` ubicado en la raíz del proyecto. Cualquier cambio en el comportamiento del sistema (umbrales, tiempos, parámetros de AWS) se realiza editando este archivo. No es necesario modificar código ni recompilar.

Al arrancar, el sistema valida automáticamente que todos los campos críticos estén correctamente configurados. Si detecta valores vacíos o placeholders sin reemplazar, aborta con un mensaje indicando exactamente qué campo falta.

## Campos del Sistema (nivel raíz)

| Campo | Tipo | Descripción | Default | Requiere llenar |
|-------|------|-------------|---------|-----------------|
| `min_instances` | int | Mínimo de instancias activas. No puede ser menor a 2 (requisito del enunciado). | 2 | No |
| `max_instances` | int | Máximo de instancias activas. Limitado a 5 por cuenta AWS Academy. | 5 | No |
| `region` | string | Región de AWS donde operar. | `us-east-1` | No |
| `monitor_s_ip` | string | IP privada de la instancia donde corre MonitorS. | Placeholder | Sí |
| `monitor_s_port` | int | Puerto gRPC donde escucha el MonitorS (Register/Deregister). | 50052 | No |
| `monitor_c_port` | int | Puerto gRPC donde escucha el MonitorC (Ping/GetMetrics/Shutdown). | 50051 | No |
| `heartbeat_check_interval_seconds` | int | Cada cuántos segundos el MonitorS hace polling a las instancias. | 10 | No |
| `heartbeat_timeout_seconds` | int | Tras cuántos segundos sin respuesta una instancia se marca como inactiva. | 30 | No |

## Campos de EC2 (`ec2_params`)

| Campo | Tipo | Descripción | Default | Requiere llenar |
|-------|------|-------------|---------|-----------------|
| `ami` | string | AMI ID de la imagen base para crear instancias nuevas. Se obtiene tras construir la AMI (ver README-AMI.md). | Placeholder | Sí |
| `instance_type` | string | Tipo de instancia EC2. | `t2.micro` | No |
| `key_name` | string | Nombre del key pair para acceso SSH. | `vockey` | No |
| `security_groups` | array de strings | IDs de los Security Groups a asignar a cada instancia nueva. | Placeholder | Sí |
| `subnet_id` | string | ID de la subnet privada donde crear las instancias. | Placeholder | Sí |
| `tags` | objeto | Tags asignados a cada instancia creada. Se usan para identificar instancias gestionadas por el ControllerASG. | `ManagedBy: ControllerASG, Project: ASG-Project2` | No |

## Políticas de Escalamiento (`policies`)

Actualmente vacío (`[]`). Está previsto para contener las políticas de scale-up y scale-down con los siguientes parámetros:

- Umbral de scale-up (70%)
- Umbral de scale-down (30%)
- Ventana de evaluación (3 muestras consecutivas)
- Cooldown entre acciones (180 segundos)

## Validación Automática

Al arrancar, el sistema ejecuta `Validate()` sobre la configuración cargada. Si alguno de los campos marcados como "Requiere llenar" contiene un placeholder o está vacío, el sistema aborta con un mensaje como: