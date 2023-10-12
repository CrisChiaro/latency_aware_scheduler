package main

import (
	"sort"
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
	data map[string]map[string]map[string]*LatencyMeasurement // userID -> appName -> nodeName -> LatencyMeasurement
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

func (l *LatencyMeasurements) GetTotalMeasurementsPerUserApp(userID, appName string) int {
	l.RLock()
	defer l.RUnlock()
	return len(l.data[userID][appName])
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

type LatencyThresholds struct {
	data map[string]int64
	sync.RWMutex
}

func NewLatencyThreshold() *LatencyThresholds {
	return &LatencyThresholds{
		data: make(map[string]int64),
	}
}
func (lt *LatencyThresholds) SetLatency(appName string, latency int64) {
	lt.Lock()
	defer lt.Unlock()
	lt.data[appName] = latency

}

func (lt *LatencyThresholds) RemoveLatency(appName string) {
	lt.Lock()
	defer lt.Unlock()
	delete(lt.data, appName)

}

func (lt *LatencyThresholds) GetLatency(appName string) (int64, bool) {
	lt.RLock()
	defer lt.RUnlock()
	value, ok := lt.data[appName]
	return value, ok
}

type NodeLatencyInfo struct {
	NodeName    string
	Measurement int64
	Timestamp   time.Time
}

func SortNodesByMeasurement(nodeMeasurements map[string]*LatencyMeasurement) []string {
	var nodeLatencyInfos []NodeLatencyInfo

	for nodeName, measurement := range nodeMeasurements {
		nodeLatencyInfos = append(nodeLatencyInfos, NodeLatencyInfo{
			NodeName:    nodeName,
			Measurement: measurement.Measurement,
			Timestamp:   measurement.Timestamp,
		})
	}

	sort.Slice(nodeLatencyInfos, func(i, j int) bool {
		if nodeLatencyInfos[i].Measurement != nodeLatencyInfos[j].Measurement {
			// Ordina prima per Measurement decrescente
			return nodeLatencyInfos[i].Measurement > nodeLatencyInfos[j].Measurement
		}
		// Poi per Timestamp crescente
		return nodeLatencyInfos[i].Timestamp.Before(nodeLatencyInfos[j].Timestamp)
	})

	orderedNodeNames := make([]string, len(nodeLatencyInfos))
	for i, info := range nodeLatencyInfos {
		orderedNodeNames[i] = info.NodeName
	}

	return orderedNodeNames
}
