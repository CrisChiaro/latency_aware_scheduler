#!/bin/bash
kubectl apply -f ../latency-aware-scheduler.yaml
sleep 6
kubectl apply -f nginx-deployment.yaml
#sleep 6
kubectl apply -f lat-meas-serv.yaml
