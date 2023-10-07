#!/bin/bash
kubectl apply -f ../latency-aware-scheduler.yaml
sleep 6
kubectl apply -f nginx-deployment.yaml
kubectl wait --for=condition=available --timeout=300s deployment/nginx-deployment
kubectl apply -f lat-meas-serv.yaml
sleep 2