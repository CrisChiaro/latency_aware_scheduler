package main

import (
	"fmt"
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
	data map[string]map[string]map[string]*LatencyMeasurement // appName -> userID -> nodeName -> LatencyMeasurement
}

func NewLatencyMeasurements() *LatencyMeasurements {
	return &LatencyMeasurements{
		data: make(map[string]map[string]map[string]*LatencyMeasurement),
	}
}

func (l *LatencyMeasurements) AddLatency(appName, userID, nodeName string, measurement *LatencyMeasurement) {
	l.Lock()
	defer l.Unlock()
	appMeasurements, ok := l.data[appName]
	if !ok {
		appMeasurements = make(map[string]map[string]*LatencyMeasurement)
		l.data[appName] = appMeasurements
	}
	userMeasurements, ok := appMeasurements[userID]
	if !ok {
		userMeasurements = make(map[string]*LatencyMeasurement)
		appMeasurements[userID] = userMeasurements
	}

	existingMeasurement, ok := userMeasurements[nodeName]
	if !ok || existingMeasurement.Timestamp.Before(measurement.Timestamp) {
		userMeasurements[nodeName] = measurement
	}
}

func (l *LatencyMeasurements) DeleteLatency(appName, userID, nodeName string) {
	l.Lock()
	defer l.Unlock()
	if appMeasurements, ok := l.data[appName]; ok {
		if userMeasurements, ok := appMeasurements[userID]; ok {
			delete(userMeasurements, nodeName)
		}
	}
}

func (l *LatencyMeasurements) GetMeasurement(appName, userID, nodeName string) (*LatencyMeasurement, bool) {
	l.RLock()
	defer l.RUnlock()
	value, ok := l.data[appName][userID][nodeName]
	return value, ok
}

func (l *LatencyMeasurements) GetMeasurements() map[string]map[string]map[string]*LatencyMeasurement {
	l.RLock()
	defer l.RUnlock()
	return l.data
}

func (l *LatencyMeasurements) GetTotalMeasurementsPerUserApp(appName, userID string) int {
	l.RLock()
	defer l.RUnlock()
	return len(l.data[appName][userID])
}

func (l *LatencyMeasurements) UpdateMeasurements(newMeasurements map[string]map[string]map[string]*LatencyMeasurement) {
	for appName, userMeasurements := range newMeasurements {
		for userID, nodeMeasurements := range userMeasurements {
			for nodeName, measurement := range nodeMeasurements {
				l.AddLatency(appName, userID, nodeName, measurement)
			}
		}
	}
}

func (l *LatencyMeasurements) CleanupMeasurementsOlderThan(minutes int) {
	l.RLock()
	defer l.RUnlock()
	podsPerApp := make(map[string]map[string]bool) // appName -> map of pods
	expirationDuration := time.Duration(minutes) * time.Minute
	for appName, appMeasurements := range l.data {
		for userID, userMeasurements := range appMeasurements {
			for nodeName, nodeMeasurement := range userMeasurements {
				if time.Since(nodeMeasurement.Timestamp) > expirationDuration {
					delete(userMeasurements, nodeName)
				}
				// Keep track of the pods for this app
				if _, ok := podsPerApp[appName]; !ok {
					podsPerApp[appName] = make(map[string]bool)
				}
				podsPerApp[appName][nodeMeasurement.PodName] = true
			}
			// Se dopo la pulizia, le misurazioni dell'app per l'utente sono vuote, allora rimuovi anche l'utente
			if len(l.data[appName][userID]) == 0 {
				delete(l.data[appName], userID)
			}
		}
		if len(l.data[appName]) == 0 {
			delete(l.data, appName)
		}
	}
	//DecreseReplicaSet??
}

type LatencyThresholds struct {
	data map[string]int64 //appName -> threshold
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

type ClusterInfo struct {
	ClusterName       string
	PodName           string
	CreatedAt         time.Time
	HasSoftConstraint bool
	latency           int64
}

type UserClusterAssociation struct {
	Data    map[string]map[string]*ClusterInfo //userID -> appName -> cluster measure
	changed bool
}

func NewUserClusterAssociation() *UserClusterAssociation {
	return &UserClusterAssociation{
		Data:    make(map[string]map[string]*ClusterInfo),
		changed: false,
	}
}

func (u *UserClusterAssociation) AddAssociation(userID, appName, clusterName string, measurement *LatencyMeasurement, isSoft bool) {
	userAssociations, ok := u.Data[userID]
	if !ok {
		userAssociations = make(map[string]*ClusterInfo)
		u.Data[userID] = userAssociations
	}

	if currentClusterInfo, exists := userAssociations[appName]; exists {
		// Aggiorna l'associazione solo se la nuova misurazione di latenza è inferiore
		if currentClusterInfo.latency > measurement.Measurement {
			fmt.Printf("Updating association for App %s: User %s from latency %d to %d\n", appName, userID, currentClusterInfo.latency, measurement.Measurement)
			currentClusterInfo.ClusterName = clusterName
			currentClusterInfo.PodName = measurement.PodName
			currentClusterInfo.latency = measurement.Measurement
			currentClusterInfo.HasSoftConstraint = isSoft
			currentClusterInfo.CreatedAt = time.Now()
			u.changed = true
		} else {
			fmt.Printf("Existing association for App %s: User %s has lower or equal latency. No update required.\n", appName, userID)
		}
	} else {
		// Se non esiste un'associazione precedente, crea una nuova
		userAssociations[appName] = &ClusterInfo{
			ClusterName:       clusterName,
			PodName:           measurement.PodName,
			latency:           measurement.Measurement,
			HasSoftConstraint: isSoft,
			CreatedAt:         time.Now(),
		}
		fmt.Printf("New association created for App %s: User %s, latency %d\n", appName, userID, measurement.Measurement)
		u.changed = true
	}
}

func (u *UserClusterAssociation) GetUserClusterAssociations() map[string]map[string]*ClusterInfo {
	return u.Data
}

func (u *UserClusterAssociation) GetUserClusterAssociation(userID, appName string) (*ClusterInfo, bool) {
	userAssociation, userExists := u.Data[userID]
	if !userExists {
		return nil, false
	}
	value, ok := userAssociation[appName]
	return value, ok
}

func (u *UserClusterAssociation) RemoveUserClusterAssiciation(userID, appName string) {
	if userAssociations, ok := u.Data[userID]; ok {
		delete(userAssociations, appName)
		u.changed = true
	}
}

func (u *UserClusterAssociation) CleanupAssociationsOlderThan(minutes int64) {
	keysToDelete := make(map[string][]string) // A map of userID to a slice of appNames to delete
	expirationDuration := time.Duration(minutes) * time.Minute

	for userID, appAssociations := range u.Data {
		for appName, clusterMeasure := range appAssociations {
			// assuming clusterMeasure.createdAt is of type time.Time
			//clusterMeasureTimestamp := clusterMeasure.createdAt.UnixNano() / int64(time.Millisecond)
			//fmt.Println("Cluster Measure created at ", clusterMeasure.createdAt, "\tconverted: ", clusterMeasureTimestamp) //debug
			//fmt.Println("ExpirationTimestamp: ", expirationTimestamp)                                                      //debug
			if time.Since(clusterMeasure.CreatedAt) > expirationDuration {
				fmt.Println("Cleaned association: ", userID, " - ", clusterMeasure.ClusterName, "(too old)") //debug
				if _, ok := keysToDelete[userID]; !ok {
					keysToDelete[userID] = make([]string, 0)
				}
				keysToDelete[userID] = append(keysToDelete[userID], appName)
				u.changed = true
			}
		}
	}
	// Delete the old associations
	for userID, appNames := range keysToDelete {
		for _, appName := range appNames {
			delete(u.Data[userID], appName)
		}
		if len(u.Data[userID]) == 0 {
			fmt.Println("The user ", userID, " has no more associations") //debug
			delete(u.Data, userID)
		}
	}

}
