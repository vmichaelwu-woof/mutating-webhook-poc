#!/usr/bin/env bash
set -euo pipefail

# =========================
#  Pretty + Safe Utilities
# =========================
if [[ -t 1 ]]; then
  BOLD="$(tput bold)"; DIM="$(tput dim)"; RESET="$(tput sgr0)"
  RED="$(tput setaf 1)"; GREEN="$(tput setaf 2)"; YELLOW="$(tput setaf 3)"; BLUE="$(tput setaf 4)"; CYAN="$(tput setaf 6)"
else
  BOLD=""; DIM=""; RESET=""; RED=""; GREEN=""; YELLOW=""; BLUE=""; CYAN=""
fi

log() {  # log LEVEL MESSAGE...
  local level="$1"; shift
  local ts; ts="$(date '+%Y-%m-%d %H:%M:%S')"
  case "$level" in
    INFO)    echo -e "${DIM}${ts}${RESET} ${BLUE}ℹ${RESET}  $*";;
    WARN)    echo -e "${DIM}${ts}${RESET} ${YELLOW}⚠${RESET}  $*";;
    ERROR)   echo -e "${DIM}${ts}${RESET} ${RED}✖${RESET}  $*";;
    SUCCESS) echo -e "${DIM}${ts}${RESET} ${GREEN}✓${RESET}  $*";;
    STEP)    echo -e "\n${BOLD}${CYAN}── $* ─────────────────────────────────────────────${RESET}";;
    *)       echo -e "${DIM}${ts}${RESET} $*";;
  esac
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { log ERROR "Required command '$1' not found in PATH."; exit 127; }
}

time_run() { # time_run "message" cmd...
  local msg="$1"; shift
  log INFO "$msg"
  local start=$(date +%s)
  if "$@"; then
    local end=$(date +%s); log SUCCESS "$msg ${DIM}(${end-start}s)${RESET}"
  else
    log ERROR "Failed: $msg"; exit 1
  fi
}

# =========================
#  Config
# =========================
DEPLOYMENT_NAME="mutating-webhook"
NAMESPACE="admission-controller"
IMAGE_TAG="mutating-webhook:latest"
RBAC_FILE="rbac.yaml"
WEBHOOK_FILE="webhook.yaml"

# =========================
#  Pre-flight
# =========================
log STEP "Pre-flight checks"
require_cmd kubectl
require_cmd docker
require_cmd minikube

kubectl cluster-info >/dev/null 2>&1 || {
  log ERROR "kubectl can't reach a cluster. Is your context configured?"
  exit 1
}

# =========================
#  Cleanup (if needed)
# =========================
log STEP "Cleanup old deployment"
if kubectl get deployment "$DEPLOYMENT_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
  log INFO "Deployment '${BOLD}${DEPLOYMENT_NAME}${RESET}' exists in ns '${BOLD}${NAMESPACE}${RESET}'. Deleting…"
  time_run "Deleting deployment $DEPLOYMENT_NAME" \
    kubectl delete deployment "$DEPLOYMENT_NAME" -n "$NAMESPACE" --cascade=foreground --wait=true --timeout=180s
else
  log WARN "Deployment '${BOLD}${DEPLOYMENT_NAME}${RESET}' not found in ns '${BOLD}${NAMESPACE}${RESET}'. Skipping delete."
fi

# =========================
#  Build image (Minikube)
# =========================
log STEP "Docker build (minikube env)"
MINIKUBE_STATUS="$(minikube status --format='{{.Host}}' 2>/dev/null || true)"
if [[ "$MINIKUBE_STATUS" != "Running" ]]; then
  log WARN "Minikube is not running. Starting…"
  time_run "Starting minikube" minikube start
fi

log INFO "Pointing Docker to minikube daemon…"
# shellcheck disable=SC2046
eval $(minikube docker-env)

time_run "Building image ${BOLD}${IMAGE_TAG}${RESET}" \
  docker build -t "$IMAGE_TAG" .

# =========================
#  Apply manifests
# =========================
log STEP "Apply Kubernetes manifests"
if [[ -f "$RBAC_FILE" ]]; then
  time_run "Applying ${RBAC_FILE}" kubectl apply -f "$RBAC_FILE"
else
  log WARN "${RBAC_FILE} not found; skipping."
fi

if [[ -f "$WEBHOOK_FILE" ]]; then
  time_run "Applying ${WEBHOOK_FILE}" kubectl apply -f "$WEBHOOK_FILE"
else
  log WARN "${WEBHOOK_FILE} not found; skipping."
fi

# =========================
#  Post-apply info
# =========================
log STEP "Post-apply status"
log INFO "Namespace: ${BOLD}${NAMESPACE}${RESET}"
log INFO "Deployment: ${BOLD}${DEPLOYMENT_NAME}${RESET}"
log INFO "Image: ${BOLD}${IMAGE_TAG}${RESET}"

if kubectl rollout status deploy/"$DEPLOYMENT_NAME" -n "$NAMESPACE" --timeout=120s; then
  log SUCCESS "Rollout successful."
else
  log WARN "Rollout may be pending or failed. Showing pods:"
  kubectl get pods -n "$NAMESPACE" -o wide
fi

log STEP "Done"
log SUCCESS "All steps completed."
echo
echo -e "${DIM}Pro tips:${RESET}
  • Follow logs:  kubectl logs -n ${NAMESPACE} -l app=${DEPLOYMENT_NAME} -f --prefix --all-containers
  • Check events: kubectl get events -n ${NAMESPACE} --sort-by=.lastTimestamp
  • Pod status:   kubectl get pods -n ${NAMESPACE} -o wide
"
