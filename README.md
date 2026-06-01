# Proyecto 2: Auto-escalador Distribuido y Resiliente en AWS EC2

### INTEGRANTES DEL EQUIPO:
*   **Mateo Andrés Amaya Cardona**
*   **Miguel Ángel Rendón Quintero**
*   **Mateo Cadavid Ramírez**

---

## 1. Descripción General del Proyecto

Este proyecto diseña e implementa un **Auto-escalador Dinámico Distribuido y Resiliente** que opera sobre instancias de computación en la nube **Amazon EC2 de AWS**. El sistema recopila métricas de uso de CPU en tiempo real de múltiples servidores virtuales activos, analiza de forma proactiva la carga de trabajo y ajusta dinámicamente la capacidad del clúster (escalamiento hacia arriba y hacia abajo) aplicando un enfoque sofisticado **Greedy Load-Aware** para garantizar la máxima eficiencia de costos y la estabilidad del clúster.

---

## 2. Arquitectura de Software: Arquitectura Hexagonal

El sistema está construido siguiendo el patrón de **Arquitectura Hexagonal (Puertos y Adaptadores)**, lo que permite desacoplar por completo el núcleo del negocio (el motor de auto-escalamiento) de los detalles de infraestructura (AWS SDK, base de datos SQLite, protocolos de red gRPC/HTTP).

```text
               ┌───────────────────────────────────────────────────┐
               │              ADAPTADORES DE ENTRADA               │
               │        (cmd/monitors, cmd/monitorc, HTTP API)     │
               └─────────┬───────────────────────────────┬─────────┘
                         │                               │
   ┌─────────────────────▼───────────────────────────────▼─────────────────────┐
   │                                  PUERTOS                                 │
   │                          (internal/ports/registry)                       │
   │                                                                          │
   │      ┌─────────────────────────────────────────────────────────────┐     │
   │      │                       DOMINIO (NÚCLEO)                      │     │
   │      │             (internal/domain/instance, log, events)         │     │
   │      │                                                             │     │
   │      │                    MOTOR DE AUTO-ESCALAMIENTO               │     │
   │      │                   (internal/controller/asg.go)              │     │
   │      └─────────────────────────────────────────────────────────────┘     │
   │                                                                          │
   └─────────────────────┬───────────────────────────────┬────────────────────┘
                         │                               │
               ┌─────────▼───────────────────────────────▼─────────┐
               │              ADAPTADORES DE SALIDA                │
               │           (AWS SDK Cloud, SQLite DB)              │
               └───────────────────────────────────────────────────┘
```

### Estructura de Directorios del Código:
*   **`cmd/`**: Puntos de entrada ejecutables.
    *   `monitors/`: Servidor central y controlador de auto-escalamiento.
    *   `monitorc/`: Agente cliente liviano que corre en cada instancia EC2 para reportar métricas.
    *   `metrics/`: Utilidad CLI interactiva para observar el clúster desde el PC local.
    *   `loadtest/`: Simulador de cargas y pruebas de estrés para el clúster.
*   **`internal/domain/`**: Entidades y lógica central. Representa la instancia y los eventos del sistema independientes de red o BD.
*   **`internal/ports/`**: Interfaces y contratos (Puertos de entrada/salida).
*   **`internal/adapters/`**: Implementaciones técnicas (Adaptadores).
    *   `cloud/`: Cliente de comunicación con el SDK de AWS EC2 (`ec2.go`, `discovery.go`, `awsretry.go`).
    *   `persistence/`: Acceso a base de datos persistente en SQLite (`sqlite.go`, `eventstore.go`, `inmemory.go`).
    *   `http/`: Endpoints de la API REST de monitoreo y auditoría (`server.go`, `handler.go`).
*   **`pkg/monitor/`**: Protocolos gRPC del ciclo de vivacidad, polling periódico y red de comunicaciones.
*   **`api/proto/`**: Definiciones de la API gRPC con buffers de protocolo (Protobuf).
*   **`deploy/`**: Scripts de automatización en la nube, user-data, servicios de systemd Linux y guías maestras de red.

---

## 3. Comportamiento del Sistema y Reglas de Negocio

El **ControllerASG** (el controlador de auto-escalamiento) ejecuta un ciclo de reconciliación recurrente cada **15 segundos** aplicando las siguientes reglas de negocio:

### A. Control de Límites del Clúster (HU-08)
El controlador lee de forma dinámica la configuración desde `config.json` para garantizar que la cantidad de instancias virtuales activas se mantenga acotada de forma segura:
*   **Mínimo de instancias (`MinInstances`):** No menor a `2` (Garantía de alta disponibilidad).
*   **Máximo de instancias (`MaxInstances`):** Máximo `5` (Restricción técnica de las cuentas AWS Academy).

### B. Escalamiento Predictivo Greedy Load-Aware (HU-26)
*   **Cálculo Predictivo:** En lugar de reaccionar lentamente añadiendo o borrando 1 sola instancia por ciclo, el sistema calcula matemáticamente cuántas instancias son necesarias para estabilizar la carga de CPU promedio del clúster en un punto medio óptimo del **50%** (el target de estabilidad entre el umbral de 30% y 70%).
    $$\text{Cantidad Deseada} = \left\lceil \frac{\text{Carga Total del Clúster}}{50\%} \right\rceil$$
*   **Estrategia Greedy (Scale Down):** Cuando el promedio de carga cae por debajo del **30%**, el sistema ordena las instancias activas por su uso de CPU real en orden **ascendente**. Se terminan primero las **instancias de menor carga**, minimizando la interrupción operativa y redistribuyendo la carga residual eficientemente.
*   **Ventana de Evaluación y Cooldown:** Para evitar el "thrashing" (escalamientos repetitivos infinitos por fluctuaciones temporales), el sistema cuenta con un `EvaluationWindow` (streak de 3 chequeos consecutivos requeridos antes de escalar) y un `Cooldown` de 180 segundos post-escala.

### C. Resiliencia y Auto-Recuperación Activa (HU-12 y HU-22)
*   **Recuperación Inmediata (HU-12):** Si la cantidad de instancias activas cae por debajo de `MinInstances` (por caídas, fallos de red o borrados manuales en AWS), el controlador ignora las métricas de carga yprioriza lanzar reemplazos de inmediato.
*   **Limpieza de Fantasmas (HU-22):** Compara el registro local contra el inventario real en AWS. Si una máquina fue destruida en AWS, la borra de inmediato de la base de datos local para mantener la consistencia.

### D. Descubrimiento Dinámico de Servidores (HU-10)
El agente `MonitorC` descubre de forma dinámica a qué IP interna conectarse para registrarse en el `MonitorS` usando una estrategia con 3 niveles de fallback:
1.  Variable de entorno `MONITOR_S_IP`.
2.  Archivo de configuración local `/etc/monitor_c.env` (inyectado por el User-Data de AWS al bootear).
3.  Consulta dinámica al API de AWS EC2 buscando instancias en estado running con el tag `Name: MonitorS*`.

---

## 4. Funciones Principales del Código

*   **`NewASGController`** (`internal/controller/asg.go`): Inicializa el controlador leyendo dinámicamente la configuración de límites, umbrales y la región de AWS.
*   **`reconcile`** (`internal/controller/asg.go`): Hilo conductor principal que corre concurrentemente con el servidor. Realiza la limpieza de fantasmas, valida la resiliencia mínima, calcula la carga del clúster y evalúa las tendencias de las métricas.
*   **`scaleUp` / `scaleDown`** (`internal/controller/asg.go`): Consume los puertos de infraestructura cloud para crear instancias EC2 o finalizarlas selectivamente por carga CPU (Greedy).
*   **`DiscoverMonitorS`** (`internal/adapters/cloud/discovery.go`): Resuelve dinámicamente la IP del servidor de control utilizando tags de la nube.
*   **`GetAggregatedMetrics`** (`internal/adapters/persistence/sqlite.go`): Calcula de forma agregada en base de datos la carga de CPU y cantidad de nodos, excluyendo dinámicamente instancias marcadas para apagado.
*   **`RecordEvent`** (`internal/adapters/persistence/eventstore.go`): Audita y persiste en SQLite los eventos de escala, severidades y fallos de infraestructura para trazabilidad.

---

## 5. Configuración e Instalación en AWS Academy

### Paso 1: Configurar la Red en AWS
1.  Crea una VPC con CIDR `172.20.0.0/16`.
2.  Crea dos subredes en la misma AZ (ej: `us-east-1a`):
    *   **Subnet Pública:** CIDR `172.20.1.0/24` (IP pública habilitada).
    *   **Subnet Privada:** CIDR `172.20.2.0/24`.
3.  Crea dos Security Groups:
    *   **`SG-Control` (para el Cerebro):** Abre puerto `22` (SSH) para tu IP personal, puerto `8080` (HTTP) para tu navegador, y puerto `50051` (gRPC) para recibir métricas.
    *   **`SG-AppInstance` (para las máquinas escaladas):** Abre puertos `22` (SSH) y `50052` (gRPC interno) permitiendo tráfico **únicamente** con origen `SG-Control`.

### Paso 2: Crear la AMI Personalizada
1.  Lanza una EC2 temporal (Ubuntu 24.04 LTS, t2.micro) en tu Subnet Pública.
2.  Compila localmente el agente para Linux:
    ```powershell
    $env:GOOS="linux"; $env:GOARCH="amd64"; go build -o monitor_c ./cmd/monitorc
    ```
3.  Sube por `scp` los archivos necesarios:
    ```powershell
    scp -i labsuser.pem monitor_c deploy/setup-ami.sh deploy/monitor-c.service deploy/appinstance.service appinstance/main.py appinstance/requirements.txt ubuntu@<IP_PUBLICA>:~/
    ```
4.  Conéctate por SSH, limpia los saltos de línea de Windows del script y ejecútalo:
    ```bash
    sed -i 's/\r$//' setup-ami.sh
    chmod +x setup-ami.sh
    ./setup-ami.sh
    ```
5.  Mueve los archivos a sus rutas de sistema definitivas:
    ```bash
    sudo cp ~/monitor_c /usr/local/bin/monitor_c
    cp ~/main.py ~/requirements.txt /opt/appinstance/
    sudo cp ~/monitor-c.service ~/appinstance.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable monitor-c appinstance
    ```
6.  Apaga la máquina y crea una imagen **AMI** desde la consola de AWS llamada `ami-asg-base-v1`. Anota el **AMI ID** y borra la máquina temporal.

### Paso 3: Configurar y Lanzar el Cerebro (MonitorS)
1.  Lanza una EC2 definitiva en la Subnet Pública, con IP fija `172.20.1.10`, asociada al grupo `SG-Control` y al perfil IAM **`LabInstanceProfile`** (o `LabRole`).
2.  En tu computadora local, edita el archivo `config.json` con los IDs reales obtenidos:
    ```json
    {
      "MonitorSIP": "172.20.1.10",
      "ec2_params": {
        "ami": "ami-tu-ami-personalizada",
        "key_name": "vockey",
        "security_groups": ["sg-tu-sg-appinstance"],
        "subnet_id": "subnet-tu-subnet-privada"
      }
    }
    ```
3.  Compila el binario del cerebro para Linux:
    ```powershell
    $env:GOOS="linux"; $env:GOARCH="amd64"; go build -o monitors ./cmd/monitors
    ```
4.  Sube los archivos y ejecútalo:
    ```powershell
    scp -i labsuser.pem monitors config.json ubuntu@<IP_PUBLICA_CEREBRO>:~/
    ssh -i labsuser.pem ubuntu@<IP_PUBLICA_CEREBRO>
    chmod +x monitors
    ./monitors
    ```

---

## 6. Guía de Pruebas para el Profesor (Verificación de Funcionalidades)

### Prueba 1: Resiliencia y Auto-Recuperación (HU-12 y HU-22)
1.  Con el programa `./monitors` corriendo en tu cerebro, ve a la consola de AWS y **apaga/termina (delete)** una de las AppInstances creadas por el controlador.
2.  Observa los logs de tu consola SSH del cerebro. Verás que en unos segundos detecta la caída de la máquina, la borra del registro para no tener fantasmas (`removing ghost instance...`) y **lanza de inmediato un reemplazo automático en AWS** para mantener el mínimo de 2 instancias.

### Prueba 2: Monitoreo Interactivo por Consola (CLI en vivo)
1.  En tu computador local, compila la utilidad de métricas para tu sistema operativo:
    ```powershell
    go build -o metrics.exe ./cmd/metrics
    ```
2.  Arráncalo en modo continuo apuntando a la IP pública de tu cerebro:
    ```powershell
    ./metrics.exe --server <IP_PUBLICA_CEREBRO>:50051 --watch
    ```
3.  Verás una consola interactiva en tiempo real que se actualiza cada 10 segundos listando la carga promedio de CPU del clúster y el estado individual de cada máquina registrada.

### Prueba 3: Auditoría y API REST mediante Navegador Web
Puedes consultar de forma visual el clúster abriendo cualquier navegador e ingresando a las siguientes URLs:
*   **Ver instancias registradas (JSON):** `http://<IP_PUBLICA_CEREBRO>:8080/instances`
*   **Ver el historial de eventos de auto-escalado auditado:** `http://<IP_PUBLICA_CEREBRO>:8080/events`
    *(Verás los logs estructurados con niveles de severidad guardados en la base de datos local).*

### Prueba 4: Correr las Pruebas Unitarias de Lógica Matemática
Para verificar la exactitud de los cálculos lógicos y la conversión de tipos en el núcleo del auto-escalador, puedes ejecutar los tests unitarios automatizados desde la raíz del proyecto en tu computadora local:
```powershell
go test -v ./internal/controller/...
```
*(Todos los tests pasarán de manera exitosa confirmando la integridad algorítmica).*
