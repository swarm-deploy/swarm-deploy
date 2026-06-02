# Drift detection

swarm-deploy checks drift on:
- Service missed from cluster
  - Service state is marked as out of sync with reason `Service Missed`
  - Event `serviceMissed` is dispatched with details: `stack_name`, `service_name`, `commit`
