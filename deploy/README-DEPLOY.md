# Guía de Despliegue Completo del Sistema

## Descripción

Este documento describe el procedimiento completo para desplegar el sistema de auto-escalamiento desde cero en AWS Academy. Sigue los pasos en orden, cada uno depende del anterior.

## Pre-requisitos

- Cuenta de AWS Academy con el laboratorio iniciado.
- Key pair `vockey` disponible.
- Código del proyecto clonado localmente.
- Binario del MonitorC compilado (ejecutar `go build -o monitor_c ./cmd/monitorc/` desde la raíz del proyecto).

## Paso 1 — Crear la VPC

Crear la VPC del proyecto con la siguiente configuración:

- Nombre: `ASG-vpc`
- CIDR: `172.20.0.0/16`
- 1 AZ: `us-east-1a`
- Subnet pública: `172.20.1.0/24` (para la instancia de control)
- Subnet privada: `172.20.2.0/24` (para las AppInstances)
- Internet Gateway: 1, conectado a la VPC
- NAT Gateway: ninguno
- Route table pública: default → IGW
- Route table privada: sin ruta a Internet

Anotar los IDs de la VPC, subnet pública y subnet privada.

## Paso 2 — Crear los Security Groups

Seguir la guía detallada en `deploy/README-SECURITY.md`.

Resultado: dos SG IDs (SG-Control y SG-AppInstance).

## Paso 3 — Construir la AMI Base

Seguir la guía detallada en `deploy/README-AMI.md`.

Resultado: un AMI ID.

## Paso 4 — Configurar el Sistema

1. Abrir `config.json`.
2. Reemplazar todos los placeholders con los valores reales obtenidos en los pasos anteriores.
3. Verificar que no queden placeholders:

```bash
grep "REPLACE_WITH" config.json
```

Si no devuelve resultados, la configuración está completa. Ver `deploy/README-CONFIG.md` para detalle de cada campo.

## Paso 5 — Lanzar la Instancia de Control

1. Lanzar una EC2 en la subnet pública:
   - AMI: Ubuntu Server 22.04 LTS
   - Tipo: t2.micro
   - Subnet: pública (172.20.1.0/24)
   - IP pública: habilitada
   - Security Group: SG-Control
   - Key pair: vockey
   - IAM Instance Profile: LabRole (si está disponible; ver decisión de credenciales en el documento de diseño)

2. Conectarse por SSH:

```bash
ssh -i vockey.pem ubuntu@<IP_PUBLICA>
```

3. Subir el proyecto:

```bash
scp -i vockey.pem -r distributed-ec2-autoscaler ubuntu@<IP>:~/
```

4. Compilar y arrancar MonitorS + ControllerASG:

```bash
cd ~/distributed-ec2-autoscaler
go build -o monitors ./cmd/monitors/
./monitors
```

## Paso 6 — Verificar el Funcionamiento

1. En la terminal del MonitorS, verificar que los logs muestran el arranque correcto y la carga de configuración.
2. Si hay instancias mínimas configuradas (min_instances=2), el ControllerASG debería empezar a crear instancias automáticamente (requiere HU-05 implementada).
3. Verificar en la consola de AWS que las instancias aparecen con los tags `ManagedBy: ControllerASG`.
4. Verificar que las AppInstances se registran en el MonitorS (visible en los logs).

## Orden de los Documentos de Referencia

| Paso | Documento | Propósito |
|------|-----------|-----------|
| VPC y red | Este documento (Paso 1) | Crear la infraestructura de red |
| Security Groups | `deploy/README-SECURITY.md` | Configurar restricciones de acceso |
| AMI | `deploy/README-AMI.md` | Construir la imagen base de las AppInstances |
| Configuración | `deploy/README-CONFIG.md` | Llenar los parámetros del sistema |
| Despliegue | Este documento (Pasos 5-6) | Lanzar el sistema |

## Cómo Recrear Todo desde Cero

Si necesitas empezar de nuevo (por ejemplo, tras un reinicio largo del laboratorio):

1. Verificar que la VPC y los Security Groups siguen existentes (no se borran con el reinicio).
2. Verificar que la AMI sigue disponible en EC2 > AMIs.
3. Actualizar `config.json` si algún ID cambió.
4. Relanzar la instancia de control (Paso 5).
5. Si usas LabRole, no necesitas actualizar credenciales. Si no, actualizar las credenciales temporales (ver documento de decisiones de diseño, sección 3.6.5).

## Estructura de Archivos de Despliegue

```
deploy/
├── README-DEPLOY.md        ← este archivo (guía maestra)
├── README-AMI.md            ← guía de construcción de la AMI
├── README-CONFIG.md         ← referencia de configuración
├── README-SECURITY.md       ← configuración de Security Groups
├── setup-ami.sh             ← script de preparación de la EC2 base
├── monitor-c.service        ← servicio systemd del MonitorC
├── appinstance.service      ← servicio systemd de la AppInstance
└── user-data.sh             ← template de user-data para instancias nuevas
```