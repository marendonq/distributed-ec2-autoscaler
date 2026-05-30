# Guía de Construcción de la AMI Base

## Descripción

Este documento describe el procedimiento para construir la AMI (Amazon Machine Image) base del proyecto de auto-escalamiento. Esta AMI es la plantilla a partir de la cual el ControllerASG crea nuevas instancias EC2 automáticamente.

## Pre-requisitos

Antes de comenzar, asegúrate de tener:

- VPC del proyecto creada (CIDR 172.20.0.0/16) con subnet pública y privada.
- Un Security Group temporal que permita SSH (puerto 22) desde tu IP.
- Key pair `vockey` disponible en tu cuenta de AWS Academy.
- El binario compilado del MonitorC (`monitor_c`) listo para subir.
- Los archivos de la AppInstance (`main.py`, `requirements.txt`) listos para subir.
- Los archivos `.service` de systemd (`monitor-c.service`, `appinstance.service`) listos para subir.

## Pasos para Construir la AMI

### 1. Lanzar la EC2 base

- AMI: Ubuntu Server 22.04 LTS
- Tipo: t2.micro
- Subnet: pública (172.20.1.0/24), con IP pública habilitada
- Security Group: el temporal con SSH
- Key pair: vockey

### 2. Conectarse por SSH

```bash
ssh -i vockey.pem ubuntu@<IP_PUBLICA_DE_LA_EC2>
```

### 3. Copiar los archivos del proyecto a la EC2

Desde tu máquina local:

```bash
scp -i vockey.pem deploy/setup-ami.sh ubuntu@<IP>:~/
scp -i vockey.pem deploy/monitor-c.service ubuntu@<IP>:~/
scp -i vockey.pem deploy/appinstance.service ubuntu@<IP>:~/
scp -i vockey.pem appinstance/main.py ubuntu@<IP>:~/
scp -i vockey.pem appinstance/requirements.txt ubuntu@<IP>:~/
scp -i vockey.pem <ruta_al_binario>/monitor_c ubuntu@<IP>:~/
```

### 4. Ejecutar el script de setup

```bash
chmod +x setup-ami.sh
./setup-ami.sh
```

### 5. Ubicar los archivos en sus directorios definitivos

```bash
sudo cp ~/monitor_c /usr/local/bin/monitor_c
sudo chmod +x /usr/local/bin/monitor_c

cp ~/main.py /opt/appinstance/main.py
cp ~/requirements.txt /opt/appinstance/requirements.txt

sudo cp ~/monitor-c.service /etc/systemd/system/monitor-c.service
sudo cp ~/appinstance.service /etc/systemd/system/appinstance.service
```

### 6. Habilitar los servicios (sin arrancarlos)

```bash
sudo systemctl daemon-reload
sudo systemctl enable monitor-c
sudo systemctl enable appinstance
```

### 7. Limpieza pre-AMI

```bash
rm -f ~/setup-ami.sh ~/monitor_c ~/main.py ~/requirements.txt
rm -f ~/monitor-c.service ~/appinstance.service
sudo rm -f /var/log/syslog* /var/log/auth.log*
history -c
```

### 8. Crear la AMI desde la consola de AWS

1. En EC2 > Instances, seleccionar la instancia base.
2. Actions > Image and templates > Create image.
3. Nombre: `ami-asg-base-v1`.
4. Esperar a que el estado sea `available` en EC2 > AMIs.
5. Anotar el AMI ID generado (ej. `ami-0abc123def456`).

### 9. Actualizar el proyecto

Poner el AMI ID en `config.json`:

```json
"ami": "ami-0abc123def456"
```

### 10. Terminar la EC2 base

Ya no se necesita. La AMI quedó guardada.

## Contenido de la AMI Resultante

- Ubuntu 22.04 LTS actualizado
- Python 3 + pip + venv
- Virtualenv en `/opt/appinstance/venv` con FastAPI y Uvicorn
- AppInstance en `/opt/appinstance/main.py`
- Binario MonitorC en `/usr/local/bin/monitor_c`
- Servicios systemd: `monitor-c.service` y `appinstance.service` (habilitados, no arrancados)
- Archivo `/etc/monitor_c.env` vacío (se llena vía user-data en cada boot)

## Cómo Actualizar la AMI

Si cambia el código del MonitorC o la AppInstance:

1. Lanzar una EC2 nueva desde la AMI actual.
2. Conectarse por SSH.
3. Reemplazar el binario o el código Python.
4. Crear una nueva AMI (`ami-asg-base-v2`, `v3`, etc.).
5. Actualizar el AMI ID en `config.json`.
6. Terminar la EC2 temporal.

## Notas Importantes

- No arrancar los servicios con `systemctl start` en la EC2 base. Solo `enable`. El start lo hace systemd automáticamente en cada instancia hija al bootear.
- El archivo `/etc/monitor_c.env` debe estar vacío en la AMI. El user-data de cada instancia lo sobrescribe con la IP real del MonitorS.
- Si el setup-ami.sh falla a mitad de ejecución, puedes volver a correrlo (los comandos son idempotentes gracias a `mkdir -p` y `apt-get install -y`).