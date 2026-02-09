# LGTM Stack - Local Observability Platform

A complete observability stack using Grafana's LGTM (Loki-Grafana-Tempo-Mimir) deployed on minikube.

## 🚀 Quick Start

```bash
# Deploy the entire stack
task up

# Check status
task status

# Verify data ingestion
task verify

# Access Grafana
open http://localhost:3000
# Username: admin
# Password: admin123
```

## 📋 Requirements

- [minikube](https://minikube.sigs.k8s.io/docs/start/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/)
- [Task](https://taskfile.dev/installation/)

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Grafana UI                           │
│                    http://localhost:3000                    │
└─────────────────────────────────────────────────────────────┘
                              │
         ┌────────────────────┼────────────────────┐
         │                    │                    │
         ▼                    ▼                    ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│    Mimir     │    │     Loki     │    │    Tempo     │
│   (Metrics)  │    │    (Logs)    │    │   (Traces)   │
└──────────────┘    └──────────────┘    └──────────────┘
```

### Components

| Component   | Purpose                    | Endpoint (internal)                             |
| ----------- | -------------------------- | ----------------------------------------------- |
| **Grafana** | Visualization & Dashboards | `grafana.observability.svc:80`                  |
| **Mimir**   | Metrics storage & query    | `mimir-gateway.observability.svc:80/prometheus` |
| **Loki**    | Log aggregation & search   | `loki-gateway.observability.svc:80`             |
| **Tempo**   | Distributed tracing        | `tempo.observability.svc:3100`                  |

## 📝 Available Tasks

### Main Commands

| Task          | Description                             |
| ------------- | --------------------------------------- |
| `task up`     | Deploy the complete LGTM stack          |
| `task down`   | Remove the entire LGTM stack            |
| `task status` | Show stack status and pod health        |
| `task verify` | Verify data ingestion in Mimir and Loki |

### Utility Commands

| Task                | Description                      |
| ------------------- | -------------------------------- |
| `task url`          | Show access URLs and credentials |
| `task logs:grafana` | View Grafana logs                |
| `task logs:mimir`   | View Mimir logs                  |
| `task logs:loki`    | View Loki logs                   |
| `task logs:tempo`   | View Tempo logs                  |
| `task test-metrics` | Push test metrics to Mimir       |
| `task test-logs`    | Push test logs to Loki           |
| `task clean`        | Remove everything including data |

## 🔌 Port Forwarding

The stack automatically sets up port-forwarding for Grafana. To manually forward other services:

```bash
# Mimir (metrics API)
kubectl port-forward svc/mimir-gateway 8080:80 -n observability

# Loki (logs API)
kubectl port-forward svc/loki-gateway 3100:80 -n observability

# Tempo (traces API)
kubectl port-forward svc/tempo 3200:3200 -n observability
```

## 📊 Pre-configured Datasources

Grafana comes with datasources pre-configured:

1. **Mimir** (Prometheus) - Default datasource for metrics
2. **Loki** - For log aggregation
3. **Tempo** - For distributed tracing

## 🔧 Configuration

Edit `values.yaml` to customize:

- Resource limits
- Retention periods
- Storage sizes
- Authentication settings

### Key Settings

```yaml
# Mimir
mimir:
  structuredConfig:
    multitenancy_enabled: false
    ingester:
      ring:
        replication_factor: 1

# Loki
loki:
  auth_enabled: false
  common:
    replication_factor: 1
  limits_config:
    retention_period: 168h # 7 days

# Grafana
grafana:
  admin:
    password: admin123 # Change in production!
```

## 📁 Directory Structure

```
lgtm-stack/
├── README.md          # This file
├── Taskfile.yml       # Task definitions
└── values.yaml        # Helm values for all components
```

## 🛠️ Troubleshooting

### Pods not starting

```bash
# Check pod status
kubectl get pods -n observability

# Check events
kubectl get events -n observability --sort-by='.lastTimestamp'

# View logs
kubectl logs -n observability -l app.kubernetes.io/name=mimir
```

### No metrics/logs appearing

```bash
# Verify data ingestion
task verify

# Check agent is collecting
kubectl get pods -n observability -l app.kubernetes.io/name=grafana-agent
```

### Port forwarding issues

```bash
# Kill existing port-forwards
pkill -f "kubectl.*port-forward"

# Restart port-forward
task port-forward
```

## 🔐 Security Notes

⚠️ **This is a development/demo configuration. Do not use in production without:**

- Enabling authentication (`multitenancy_enabled: true`)
- Changing default passwords
- Using TLS certificates
- Setting up proper RBAC
- Configuring network policies

## 🧹 Cleanup

```bash
# Remove stack (keeps data)
task down

# Remove everything including data
task clean

# Stop minikube
minikube stop

# Delete minikube
minikube delete
```

## 📚 Documentation

- [Grafana Mimir](https://grafana.com/docs/mimir/latest/)
- [Grafana Loki](https://grafana.com/docs/loki/latest/)
- [Grafana Tempo](https://grafana.com/docs/tempo/latest/)
- [Grafana Docs](https://grafana.com/docs/grafana/latest/)

## 🤝 Contributing

This is a local development setup. For production use, consider:

- External object storage (S3, GCS, Azure Blob)
- High availability setup (multiple replicas)
- Proper backup and disaster recovery
- Monitoring of the monitoring stack (meta-monitoring)

---

**Built with ❤️ using Task, Helm, and Grafana's LGTM stack**
