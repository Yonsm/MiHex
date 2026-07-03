#!/bin/sh
# MiHex — OpenWrt / MiWiFi environment setup script (for reference only, do not run unedited)
set -e

# ── pip mirror (Aliyun) ──
mkdir -p ~/.pip
cat <<EOF > ~/.pip/pip.conf
[global]
index-url = https://mirrors.aliyun.com/pypi/simple/
cache-dir = /tmp/.cache
no-compile = true
EOF

# ── Python 3 ──
opkg update
opkg install python3 python3-pip

# ── Broadlink dependency (cryptography must be installed via opkg pre-built) ──
opkg install python3-cryptography

# ── Python dependencies ──
pip3 install --no-cache-dir --no-compile -r requirements.txt
pip3 install --no-cache-dir --no-compile mihex

# ── Mosquitto (MQTT broker, optional) ──
opkg install mosquitto-ssl
cat <<EOF > mosquitto.conf
listener 1883
allow_anonymous true
EOF
#mosquitto -d -c mosquitto.conf

# ── Configuration file ──
if [ ! -f conf.yaml ]; then
	cp mihex/conf.yaml.example conf.yaml
	echo "Generated conf.yaml — please edit it before running: mihex"
fi

# ── Clean up pip cache ──
rm -rf /tmp/.cache