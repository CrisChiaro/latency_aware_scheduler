package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Descheduler struct {
	clientset           *kubernetes.Clientset
	latencyMeasurements *LatencyMeasurements
	pauseDescheduler    chan PauseSignal
	pausedApps          map[string]bool
	thresholds          map[string]struct {
		Threshold int
		UpdatedAt time.Time
	}
}

func NewDescheduler(clientset *kubernetes.Clientset, latencyMeasurements *LatencyMeasurements, pauseDescheduler chan PauseSignal) *Descheduler {
	return &Descheduler{
		clientset:           clientset,
		latencyMeasurements: latencyMeasurements,
		pauseDescheduler:    pauseDescheduler,
		pausedApps:          make(map[string]bool),
		thresholds: make(map[string]struct {
			Threshold int
			UpdatedAt time.Time
		}),
	}
}

func (d *Descheduler) Run() {
	checkInterval := 30 * time.Second

	for {
		time.Sleep(checkInterval)

		//Controllo cancale per vedere se andare in pausa o meno
		select {
		case pauseSignal := <-d.pauseDescheduler: //modo per leggere un canale
			if pauseSignal.isPaused {
				// Aggiunge l'AppID alla lista delle app in pausa
				d.pausedApps[pauseSignal.appName] = true
			} else {
				// Rimuove l'AppID dalla lista delle app in pausa
				delete(d.pausedApps, pauseSignal.appName)
			}
		default:
		}

		fmt.Println("\nDescheduler: Trying getting new measurements:")
		// Get latency measurements from sentinel pod (latency meter)
		latencyMeasurements, err := d.getLatencyMeasurements()
		if err != nil {
			fmt.Printf("Error getting latency measurements: %v\n", err)
			continue
		}
		d.latencyMeasurements.UpdateMeasurements(latencyMeasurements)
		fmt.Printf("Current latency measurements: %v\n", d.latencyMeasurements.GetMeasurements()) //debug

		for userID, appMeasurements := range d.latencyMeasurements.GetMeasurements() {
			fmt.Println("User: ", userID) //DEBUG
			for appName, podMeasurements := range appMeasurements {
				fmt.Println("App: ", appName) //DEBUG
				/*SKIPPA SE APP COMPLETATA*/
				if _, ok := d.pausedApps[appName]; ok {
					fmt.Println("DESCHEDULER BLOCCATO PER QUESTA APP, SKIP...") //DEBUG
					continue
				}
				descheduleThreshold, err := d.GetDescheduleThreshold(appName)
				if err != nil {
					fmt.Println("Error getting deschedule threshold: ", err) //DEBUG
					return
				}
				fmt.Println("Descheduling Threshold: ", descheduleThreshold) //DEBUG
				if len(podMeasurements) >= descheduleThreshold {
					// Get the worst performing pod
					worstPodName, worstMeasurement := d.findWorstPod(podMeasurements)

					// Deschedule the worst performing pod
					pod, err := d.clientset.CoreV1().Pods(worstMeasurement.PodNamespace).Get(context.Background(), worstPodName, metav1.GetOptions{})
					if err != nil {
						fmt.Printf("Error in getting pod %s: %v\n", worstPodName, err)
						continue
					}

					err = d.clientset.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
					if err != nil {
						fmt.Printf("Error in deleting pod %s: %v\n", pod.Name, err)
					} else {
						fmt.Printf("Descheduled pod %s with latency %d ms\n", pod.Name, worstMeasurement.Measurement)
						d.latencyMeasurements.DeleteLatency(userID, appName, worstPodName)
					}
				}
				fmt.Println() //DEBUG
			}
		}
	}
}

func (d *Descheduler) getLatencyMeasurements() (map[string]map[string]map[string]*LatencyMeasurement, error) {
	pods, err := d.clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error listing pods: %v", err)
	}

	// Initialize the measurements map
	measurements := make(map[string]map[string]map[string]*LatencyMeasurement)

	for _, pod := range pods.Items {
		// Add any necessary filters here, e.g., by labels or namespace
		if pod.Namespace == "kube-system" || strings.HasPrefix(pod.Name, "local-path-provisioner") || len(pod.Status.PodIP) == 0 { //scarto i pod di sistema o non ancora schedulati
			continue
		}
		endpoint := fmt.Sprintf("http://%s:8080/measurements", pod.Status.PodIP)
		fmt.Println("Contacting: ", endpoint) //DEBUG
		resp, err := http.Get(endpoint)
		if err != nil {
			fmt.Printf("Error getting latency measurements from pod %s: %v\n", pod.Name, err)
			continue
		}
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("Error reading response body from pod %s: %v\n", pod.Name, err)
			continue
		}

		var currentPodMeasurements map[string]*LatencyMeasurement //user measurements for this pod
		err = json.Unmarshal(body, &currentPodMeasurements)
		if err != nil {
			fmt.Printf("Error unmarshaling latency measurements from pod %s: %v\n", pod.Name, err)
			continue
		}

		appName, ok := pod.Labels["app"]
		if !ok {
			return nil, fmt.Errorf("unable to determine app name from pod labels")
		}

		// Merge the podMeasurements into the overall measurements map
		for userID, userMeasurements := range currentPodMeasurements {
			if _, exists := measurements[userID]; !exists {
				measurements[userID] = make(map[string]map[string]*LatencyMeasurement)
			}
			if _, exists := measurements[userID][appName]; !exists {
				measurements[userID][appName] = make(map[string]*LatencyMeasurement)
			}
			measurements[userID][appName][pod.Name] = userMeasurements
		}
	}

	return measurements, nil
}

func (d *Descheduler) findWorstPod(podMeasurements map[string]*LatencyMeasurement) (string, *LatencyMeasurement) {
	var worstPodName string
	var worstMeasurement *LatencyMeasurement
	for podName, measurement := range podMeasurements {
		fmt.Println("Pod: ", podName)                           //DEBUG
		fmt.Println("PodNamespace: ", measurement.PodNamespace) //DEBUG
		fmt.Println("Measurement: ", measurement.Measurement)   //DEBUG
		if worstMeasurement == nil || measurement.Measurement > worstMeasurement.Measurement {
			worstPodName = podName
			worstMeasurement = measurement
		} else if measurement.Measurement == worstMeasurement.Measurement {
			if measurement.Timestamp.Before(worstMeasurement.Timestamp) {
				worstPodName = podName
				worstMeasurement = measurement
			}
		}
	}
	return worstPodName, worstMeasurement
}

func (d *Descheduler) GetDescheduleThreshold(appName string) (int, error) {

	now := time.Now()

	if data, ok := d.thresholds[appName]; ok && now.Sub(data.UpdatedAt) < time.Minute*5 {
		// Use the cached value
		return data.Threshold, nil
	}

	nsList, err := d.clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return -1, err
	}

	nodes, err := d.clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return -1, err
	}
	fmt.Println("Calculating descheduling threshold for the app: ", appName) //DEBUG
	for _, n := range nsList.Items {
		if n.Name == "kube-system" {
			continue
		}

		fmt.Printf("Checking namespace: %s\n", n.Name)
		deploymentList, err := d.clientset.AppsV1().Deployments(n.Name).List(context.Background(), metav1.ListOptions{
			//LabelSelector: fmt.Sprintf("app in (%s)", strings.ReplaceAll(appName, " ", "")),
		})
		if err != nil {
			fmt.Printf("Error getting deployments in namespace %s: %v\n", n.Name, err)
			continue
		}

		fmt.Printf("Deployments in namespace %s: \n", n.Name)

		for _, deployment := range deploymentList.Items {
			fmt.Printf("Checking deployment: %s, \tLabelSelector: %s\n", deployment.Name, deployment.Spec.Selector.MatchLabels)

			nReplicas := int(*deployment.Spec.Replicas)
			nNodes := len(nodes.Items) - 1
			// return the max between number of nodes and number of replicas
			if nReplicas < nNodes {
				fmt.Printf("Found app %s, replicas: %d, nodes: %d\n", appName, nReplicas, nNodes)
				d.thresholds[appName] = struct {
					Threshold int
					UpdatedAt time.Time
				}{
					Threshold: nReplicas,
					UpdatedAt: now,
				}

				return nReplicas, nil
			}
			// Update the cache with the new value and the current timestamp
			d.thresholds[appName] = struct {
				Threshold int
				UpdatedAt time.Time
			}{
				Threshold: nNodes,
				UpdatedAt: now,
			}
			return nNodes, nil
		}
	}
	// if I'm here I didn't find the app!
	return -1, fmt.Errorf("App not found!")
}
