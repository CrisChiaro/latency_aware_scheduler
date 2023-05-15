package main

import (
	"sync"
	"time"
)

type LatencyMeasurement struct {
	NodeName    string
	Measurement int64
	Timestamp   time.Time
}

type LatencyMeasurements struct {
	sync.RWMutex
	data map[string]map[string]*LatencyMeasurement
}

func NewLatencyMeasurements() *LatencyMeasurements {
	return &LatencyMeasurements{
		data: make(map[string]map[string]*LatencyMeasurement),
	}
}

func (l *LatencyMeasurements) AddLatency(appName, podName string, measurement *LatencyMeasurement) {
	l.Lock()
	defer l.Unlock()
	appMeasurements, ok := l.data[appName]
	if !ok {
		appMeasurements = make(map[string]*LatencyMeasurement)
		l.data[appName] = appMeasurements
	}
	appMeasurements[podName] = measurement
}

func (l *LatencyMeasurements) DeleteLatency(appName, podName string) {
	l.Lock()
	defer l.Unlock()
	if appMeasurements, ok := l.data[appName]; ok {
		delete(appMeasurements, podName)
	}
}

func (l *LatencyMeasurements) GetMeasurements() map[string]map[string]*LatencyMeasurement {
	l.RLock()
	defer l.RUnlock()
	return l.data
}
