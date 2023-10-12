package main

import (
	"sync"
	"time"
)

type LatencyMeasurement struct {
	PodNamespace string
	PodName      string
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

func (l *LatencyMeasurements) AddLatency(userID, appName, nodeName string, measurement *LatencyMeasurement) {
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

	existingMeasurement, ok := appMeasurements[nodeName]
	if !ok || existingMeasurement.Timestamp.Before(measurement.Timestamp) {
		appMeasurements[nodeName] = measurement
	}
}

func (l *LatencyMeasurements) DeleteLatency(userID, appName, nodeName string) {
	l.Lock()
	defer l.Unlock()
	if userMeasurements, ok := l.data[userID]; ok {
		if appMeasurements, ok := userMeasurements[appName]; ok {
			delete(appMeasurements, nodeName)
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
		for appName, nodeMeasurements := range appMeasurements {
			for nodeName, measurement := range nodeMeasurements {
				l.AddLatency(userID, appName, nodeName, measurement)
			}
		}
	}
}
