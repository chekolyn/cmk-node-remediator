# CMK Node Remediation

A Go K8s CronJob that automatically cordons, drains, and takes configurable
remediation actions (vm-reset, vm-stop, vm-start, vm-delete) on B200 GPU nodes
approaching the NVIDIA 60-day NVLink firmware bug threshold.

## Building

`make build`

## Testing

`make test`

## Docker

`make docker-build IMAGE_TAG=v0.1.0`
