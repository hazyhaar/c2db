#!/usr/bin/env bash
# ==============================================================================
# c2db — Script de Déploiement vers horos-prod (37.187.150.79)
# Cible : Sous-domaine c2db.hazyhaar.fr (Port local 8556)
# ==============================================================================

set -euo pipefail

TARGET_HOST="horos-prod"
OPT_DIR="/opt/c2db"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "=== 1. Compilation du binaire Linux pur Go 1.27 statique (CGO_ENABLED=0) ==="
cd "${ROOT_DIR}"
GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/c2db ./cmd/c2db
echo "✓ Binaire compilé avec succès : bin/c2db ($(du -h bin/c2db | cut -f1))"

echo "=== 2. Préparation du répertoire distant sur ${TARGET_HOST} ==="
ssh "${TARGET_HOST}" "sudo mkdir -p ${OPT_DIR} && sudo chown -R ubuntu:ubuntu ${OPT_DIR}"

echo "=== 3. Transfert du binaire statique et du service systemd ==="
scp "${ROOT_DIR}/bin/c2db" "${TARGET_HOST}:/tmp/c2db"
ssh "${TARGET_HOST}" "sudo mv /tmp/c2db ${OPT_DIR}/c2db && sudo chmod 0755 ${OPT_DIR}/c2db && sudo chown root:root ${OPT_DIR}/c2db"

scp "${SCRIPT_DIR}/c2db.service" "${TARGET_HOST}:/tmp/c2db.service"
ssh "${TARGET_HOST}" "sudo mv /tmp/c2db.service /etc/systemd/system/c2db.service && sudo systemctl daemon-reload"

echo "=== 4. Configuration Nginx pour c2db.hazyhaar.fr ==="
scp "${SCRIPT_DIR}/nginx-c2db.conf" "${TARGET_HOST}:/tmp/c2db.conf"
ssh "${TARGET_HOST}" "sudo mv /tmp/c2db.conf /etc/nginx/sites-available/c2db.hazyhaar.fr && sudo ln -sf /etc/nginx/sites-available/c2db.hazyhaar.fr /etc/nginx/sites-enabled/ && sudo nginx -t && sudo systemctl reload nginx"

echo "=== 5. Activation et démarrage de c2db.service ==="
ssh "${TARGET_HOST}" "sudo systemctl enable --now c2db.service && sudo systemctl restart c2db.service"

echo "=== 6. Vérification du statut HTTP local sur .79 ==="
ssh "${TARGET_HOST}" "sleep 1 && curl -s http://127.0.0.1:8556/health" || true

echo ""
echo "=== Déploiement achevé avec succès sur ${TARGET_HOST} ==="
echo "Portail web et simulateur c2db actifs en arrière-plan (127.0.0.1:8556)."
