# Kubernetes Deployment Guide for TDS Simulation

## Setup Steps

### 1. **Build Docker Images**

```powershell
cd k8s
.\build-images.ps1
```

This builds all 8 Docker images needed for the simulation. Verify with:
```powershell
docker images | grep sim-
docker images | grep tds-
```

### 2. **Deploy to Kubernetes**

Deploy everything:
```powershell
.\deploy.ps1
```

The deploy script now syncs a Kubernetes secret named `tds-tls-certs` from local certificate files in `certs/`:
- `certs/server.crt`
- `certs/server.key`
- `certs/client.crt`
- `certs/client.key`
- `certs/ca.crt`

If these files are missing, generate them first:
```powershell
.\scripts\generate_certs.ps1
```

Or deploy specific services:
```powershell
# Infrastructure only (tds-server, client-proxy, databases)
.\deploy.ps1 -Service infrastructure

# Individual services
.\deploy.ps1 -Service cs
.\deploy.ps1 -Service pctrbo
.\deploy.ps1 -Service pa
.\deploy.ps1 -Service station
.\deploy.ps1 -Service ticketdistributor
.\deploy.ps1 -Service gate
```

### 3. **Monitor Deployment**

Check status:
```powershell
.\deploy.ps1 -Status
```

Watch pods starting up:
```powershell
kubectl get pods -n tds-simulation -w
```

Watch a specific pod:
```powershell
kubectl logs -n tds-simulation deployment/cs -f
```

### 4. **Access Services**

Each service is deployed as a `NodePort` service. Get the node port:
```powershell
kubectl get svc -n tds-simulation
```

Example output:
```
NAME                  TYPE       CLUSTER-IP      EXTERNAL-IP   PORT(S)
cs                    NodePort   10.96.X.X       <none>        9101:31234/TCP
pctrbo                NodePort   10.96.X.X       <none>        9102:31235/TCP
pa                    NodePort   10.96.X.X       <none>        9103:31236/TCP
station-1             NodePort   10.96.X.X       <none>        9100:31237/TCP
ticketdistributor     NodePort   10.96.X.X       <none>        9110:31238/TCP
gate                  NodePort   10.96.X.X       <none>        9200:31239/TCP
```

Access from your machine:
```powershell
# Example: access CS service on the NodePort
curl http://localhost:31234
```

Or find the Kubernetes node IP and use that:
```powershell
kubectl get nodes -o wide
```

### 5. **Cleanup**

Delete all services:
```powershell
.\deploy.ps1 -Delete
```

Delete specific service:
```powershell
.\deploy.ps1 -Delete -Service cs
```

## Key Features

✅ **Service Dependencies** — Services wait for their dependencies to be ready via init containers
✅ **Persistent Storage** — PostgreSQL databases use persistent volumes
✅ **ConfigMaps** — Centralized configuration for all services
✅ **Health Checks** — Liveness probes for database containers
✅ **Namespace Isolation** — All services in `tds-simulation` namespace

## Simulating Different Machines

Each service runs in its own pod, which can be scheduled on different nodes. To actually spread them across nodes:

```powershell
# Add node affinity to deployments to force scheduling on specific nodes
# See the "Advanced: Multi-Node Setup" section below
```

### Advanced: Multi-Node Setup

For true multi-machine simulation, use node affinity. Edit any service manifest (e.g., `08-cs-service.yaml`):

```yaml
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cs
  template:
    # ... existing spec ...
    spec:
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: kubernetes.io/hostname
                operator: In
                values:
                - node-1  # Specific node hostname
      containers:
      # ... rest of container spec ...
```

Then deploy to a multi-node cluster (join additional nodes with `kubeadm join`).

## Kubernetes Setup Reference

If you need to set up Kubernetes initially:

### Docker Desktop (Built-in)
Settings → Kubernetes → Enable Kubernetes

### Minikube (Local VM)
```powershell
minikube start
minikube docker-env | Invoke-Expression
```

### Kind (Kubernetes in Docker)
```powershell
kind create cluster
```

## Useful kubectl Commands

```powershell
# Get all resources
kubectl get all -n tds-simulation

# View pod logs
kubectl logs -n tds-simulation pod/cs-<hash>

# Port forward to access service locally
kubectl port-forward -n tds-simulation svc/cs 9101:9101

# Execute command in pod
kubectl exec -it -n tds-simulation pod/cs-<hash> -- /bin/bash

# Describe resource (troubleshooting)
kubectl describe -n tds-simulation pod/cs-<hash>

# Delete resource
kubectl delete -n tds-simulation pod/cs-<hash>

# View events (useful for debugging startup issues)
kubectl get events -n tds-simulation --sort-by='.lastTimestamp'
```
