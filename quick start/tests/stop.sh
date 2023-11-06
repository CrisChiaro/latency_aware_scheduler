#!/bin/bash

kubectl delete -f ../latency-aware-scheduler.yaml
kubectl delete -f nginx-deployment.yaml
kubectl delete -f lat-meas-serv.yaml
kubectl delete -f ../routing-manager.yaml
