package main

import (
	"sync"
	"time"
)

type LatencyMeasurement struct {
	PodNamespace string
	NodeName     string
	Measurement  int64
	Timestamp    time.Time
}

type LatencyMeasurements struct {
	sync.RWMutex
	data map[string]map[string]map[string]*LatencyMeasurement // userID -> appName -> podID -> LatencyMeasurement
}

func NewLatencyMeasurements() *LatencyMeasurements {
	return &LatencyMeasurements{
		data: make(map[string]map[string]map[string]*LatencyMeasurement),
	}
}

func (l *LatencyMeasurements) AddLatency(userID, appName, podName string, measurement *LatencyMeasurement) {
	l.Lock()
	defer l.Unlock()
	userMeasurements, ok := l.data[userID]
	if !ok {
		userMeasurements = make(map[string]map[string]*LatencyMeasurement)
		l.data[userID] = userMeasurements
	}
	appMeasurements, ok := userMeasurements[appName]
	if !ok {
		appMeasurements = make(map[string]*LatencyMeasurement)
		userMeasurements[appName] = appMeasurements
	}
	appMeasurements[podName] = measurement
}

func (l *LatencyMeasurements) DeleteLatency(userID, appName, podName string) {
	l.Lock()
	defer l.Unlock()
	if userMeasurements, ok := l.data[userID]; ok {
		if appMeasurements, ok := userMeasurements[appName]; ok {
			delete(appMeasurements, podName)
		}
	}
}

func (l *LatencyMeasurements) GetMeasurements() map[string]map[string]map[string]*LatencyMeasurement {
	l.RLock()
	defer l.RUnlock()
	return l.data
}

func (l *LatencyMeasurements) UpdateMeasurements(newMeasurements map[string]map[string]map[string]*LatencyMeasurement) {
	for userID, appMeasurements := range newMeasurements {
		for appName, podMeasurements := range appMeasurements {
			for podName, measurement := range podMeasurements {
				l.AddLatency(userID, appName, podName, measurement)
			}
		}
	}
}
