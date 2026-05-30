#!/bin/bash
# setup-ami.sh - Prepara una instancia EC2 Ubuntu para servir como AMI base
# del proyecto ASG. Instala Python, crea el venv de la AppInstance y deja
# /etc/monitor_c.env listo para ser sobrescrito por user-data en cada boot.

set -euo pipefail

echo "==> Updating system packages"
sudo apt-get update -y
sudo apt-get upgrade -y


echo "==> Installing Python 3 and dependencies"
sudo apt-get install -y python3 python3-pip python3-venv


echo "==> Setting up application directory and permissions"
sudo mkdir -p /opt/appinstance
sudo chown -R ubuntu:ubuntu /opt/appinstance


echo "==> Setting up Python virtual environment and installing dependencies"
sudo -u ubuntu python3 -m venv /opt/appinstance/venv
sudo -u ubuntu /opt/appinstance/venv/bin/pip install --upgrade pip
sudo -u ubuntu /opt/appinstance/venv/bin/pip install -r /opt/appinstance/requirements.txt


sudo touch /etc/monitor_c.env
sudo chmod 644 /etc/monitor_c.env
echo "Setup complete"

