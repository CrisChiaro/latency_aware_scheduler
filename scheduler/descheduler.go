package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Descheduler struct {
	clientset           *kubernetes.Clientset
	latencyMeasurements *LatencyMeasurements
	pauseDescheduler    chan bool
	pause               bool
}

func NewDescheduler(clientset *kubernetes.Clientset, latencyMeasurements *LatencyMeasurements, pauseDescheduler chan bool) *Descheduler {
	return &Descheduler{
		clientset:           clientset,
		latencyMeasurements: latencyMeasurements,
		pauseDescheduler:    pauseDescheduler,
		pause:               false,
	}
}

func (d *Descheduler) Run() {
	descheduleThreshold := 2
	checkInterval := 10 * time.Second

	for {
		time.Sleep(checkInterval)

		//Controllo cancale per vedere se andare in pausa o meno
		select {
		case pause := <-d.pauseDescheduler:
			d.pause = pause
		default:
		}

		if d.pause {
			continue
		}

		// Get latency measurements from sentinel pod
		latencyMeasurements, err := d.getLatencyMeasurements()
		if err != nil {
			fmt.Printf("Error getting latency measurements: %v\n", err)
			continue
		}

		fmt.Printf("Current latency measurements: %v\n", latencyMeasurements) //debug

		for appName, appMeasurements := range latencyMeasurements {
			for podName, measurement := range appMeasurements {
				if measurement.Measurement >= int64(descheduleThreshold) {
					pod, err := d.clientset.CoreV1().Pods("").Get(context.Background(), podName, metav1.GetOptions{})
					if err != nil {
						fmt.Printf("Error in getting pod %s: %v\n", podName, err)
						continue
					}

					err = d.clientset.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
					if err != nil {
						fmt.Printf("Error in deleting pod %s: %v\n", pod.Name, err)
					} else {
						fmt.Printf("Descheduled pod %s with latency %d ms\n", pod.Name, measurement.Measurement)
						d.latencyMeasurements.DeleteLatency(appName, podName)
					}
				}
			}
		}
	}
}

func (d *Descheduler) getLatencyMeasurements() (map[string]map[string]*LatencyMeasurement, error) {
	// Use the service name instead of the sentinel pod's IP.
	//endpoint := "http://latency-measurer.default.svc.cluster.local:8080/measurements"
	//endpoint := "http://latency-measurer.default:8080/measurements"
	endpoint := "http://10.96.52.121:8080/measurements"

	resp, err := http.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Error getting latency measurements: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Error reading response body: %v", err)
	}
	fmt.Println("Descheduler, misures received: ", body) //DEBUG
	var measurements map[string]map[string]*LatencyMeasurement
	err = json.Unmarshal(body, &measurements)
	if err != nil {
		return nil, fmt.Errorf("Error unmarshaling latency measurements: %v", err)
	}

	return measurements, nil
}
