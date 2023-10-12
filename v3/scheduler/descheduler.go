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
	/*
		thresholds          map[string]struct {
			Threshold int
			UpdatedAt time.Time
		}
	*/
	invalidNodes          *LatencyMeasurements
	hardValidNodes        *LatencyMeasurements
	softValidNodes        *LatencyMeasurements
	hardLatencyThresholds *LatencyThresholds
	softLatencyThresholds *LatencyThresholds
}

func NewDescheduler(clientset *kubernetes.Clientset, latencyMeasurements *LatencyMeasurements, hardLatencyThresholds, softLatencyThresholds *LatencyThresholds) *Descheduler {
	return &Descheduler{
		clientset:           clientset,
		latencyMeasurements: latencyMeasurements,
		/*
			pauseDescheduler:    pauseDescheduler,
			pausedApps: make(map[string]bool),
			thresholds: make(map[string]struct {
				Threshold int
				UpdatedAt time.Time
			}),
			//pastMeasurements:  pastMeasurements,
		*/
		invalidNodes:          NewLatencyMeasurements(),
		hardValidNodes:        NewLatencyMeasurements(),
		softValidNodes:        NewLatencyMeasurements(),
		hardLatencyThresholds: hardLatencyThresholds,
		softLatencyThresholds: softLatencyThresholds,
	}
}

func (d *Descheduler) Run() {
	checkInterval := 30 * time.Second
	N_tot, err := d.getTotalNodes()
	if err != nil {
		fmt.Printf("Error getting totNodes: %v\n", err)
		return
	}

	for {
		time.Sleep(checkInterval)
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
			for appName, nodesMeasurements := range appMeasurements {
				fmt.Println("User: ", userID) //DEBUG
				fmt.Println("App: ", appName) //DEBUG
				fmt.Println("Measurements: ") //DEBUG
				/*DEBUG*/
				for nodeName, nodeMeasurements := range nodesMeasurements {
					fmt.Println(nodeName, ": ", nodeMeasurements.Measurement) //DEBUG
				}
				err := d.descheduleInvalidNodes(userID, appName, nodesMeasurements)
				if err != nil {
					fmt.Printf("Error descheduling pods in the InvalideNodes: %v\n", err)
				}
				//N_invalid := d.invalidNodes.GetTotalMeasurementsPerUserApp(userID, appName)
				//N_hardValid := d.hardValidNodes.GetTotalMeasurementsPerUserApp(userID, appName)
				//N_softValid := d.softValidNodes.GetTotalMeasurementsPerUserApp(userID, appName)
				//N_misured := N_invalid + N_hardValid + N_softValid
				//fmt.Println("Tot Misure: ", N_misured) //DEBUG
				_, needSoftCondition := d.softLatencyThresholds.GetLatency(appName)
				fmt.Println("Soft Condition: ", needSoftCondition) //DEBUG
				if needSoftCondition /*&& N_misured == N_tot*/ {   //Se ho una soft contraint: CICLO FINALE per i soft nodes
					fmt.Println("Checking the Soft Condition...") //DEBUG
					d.descheduleWorstHardValidNodes(N_tot, userID, appName)
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
		if strings.HasPrefix(pod.Namespace, "kube") || strings.HasPrefix(pod.Namespace, "liqo") || strings.HasPrefix(pod.Namespace, "metallb") || strings.HasPrefix(pod.Namespace, "local") || len(pod.Status.PodIP) == 0 { //scarto i pod di sistema o non ancora schedulati
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
			// Check if the measurement map for this user and app is nil, if it is, initialize it
			if measurements[userID][appName] == nil {
				measurements[userID][appName] = make(map[string]*LatencyMeasurement)
			}

			if measurements[userID][appName][pod.Spec.NodeName] == nil || measurements[userID][appName][pod.Spec.NodeName].Timestamp.Before(userMeasurements.Timestamp) {
				// if there was already a measurement, i check the timestamp
				measurements[userID][appName][pod.Spec.NodeName] = userMeasurements
			}
		}
	}

	return measurements, nil
}

func (d *Descheduler) DescheduleAllPodsPerNode(appName string, nodeName string) error {
	// Get the list of pods on the worst performing node
	pods, err := d.clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})

	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		// Add any necessary filters here, e.g., by labels or namespace
		if strings.HasPrefix(pod.Namespace, "kube") || strings.HasPrefix(pod.Namespace, "liqo") || strings.HasPrefix(pod.Namespace, "metallb") || strings.HasPrefix(pod.Namespace, "local") || len(pod.Status.PodIP) == 0 { //scarto i pod di sistema o non ancora schedulati
			continue
		}
		currentAppName, ok := pod.Labels["app"]
		if !ok {
			continue
		}
		if currentAppName != appName {
			continue
		}
		// Check if the pod's deletion policy allows it to be deleted. If not, skip to the next pod.
		if pod.DeletionGracePeriodSeconds != nil && *pod.DeletionGracePeriodSeconds != 0 {
			continue
		}

		err = d.clientset.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
		if err != nil {
			// Log error and continue with next pod
			fmt.Println("Failed to delete pod", pod.Name, "with error", err.Error())
			continue
		}
		fmt.Println("Successfully deleted pod", pod.Name)
	}
	return nil
}

func (d *Descheduler) getTotalNodes() (int, error) {
	nodes, err := d.clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return -1, err
	}
	return len(nodes.Items) - 1, nil
}

func (d *Descheduler) descheduleInvalidNodes(userID string, appName string, nodesMeasurements map[string]*LatencyMeasurement) error {
	h, hExists := d.hardLatencyThresholds.GetLatency(appName)
	s, sExists := d.softLatencyThresholds.GetLatency(appName)

	for nodeName, latency := range nodesMeasurements {
		if hExists && latency.Measurement <= h { //hard valid node
			d.handleValidNode(userID, appName, nodeName, latency, s, sExists)
		} else if hExists { //invalid node
			d.handleInvalidNode(userID, appName, nodeName, latency)
		} else if sExists { //hard contraint not exists
			d.handleSoftOnlyNode(userID, appName, nodeName, latency, s)
		}
	}
	return nil
}

func (d *Descheduler) handleValidNode(userID string, appName string, nodeName string, latency *LatencyMeasurement, s int64, sExists bool) {
	d.invalidNodes.DeleteLatency(userID, appName, nodeName)

	if sExists { // Check if exists soft constraint
		if latency.Measurement <= s { // SOFT valid node
			d.softValidNodes.AddLatency(userID, appName, nodeName, latency)
			d.hardValidNodes.DeleteLatency(userID, appName, nodeName)
		} else { // JUST HARD valid node
			d.hardValidNodes.AddLatency(userID, appName, nodeName, latency)
			d.softValidNodes.DeleteLatency(userID, appName, nodeName)
		}
		//d.invalidNodes.DeleteLatency(userID, appName, nodeName)
	} else { // JUST HARD valid node
		d.hardValidNodes.AddLatency(userID, appName, nodeName, latency)
	}
}

func (d *Descheduler) handleInvalidNode(userID string, appName string, nodeName string, latency *LatencyMeasurement) {
	fmt.Println(nodeName, " is an invalid node.")
	d.DescheduleAllPodsPerNode(appName, nodeName)
	//d.invalidNodes.AddLatency(userID, appName, nodeName, latency)
	d.latencyMeasurements.DeleteLatency(userID, appName, nodeName)
	d.hardValidNodes.DeleteLatency(userID, appName, nodeName)
	d.softValidNodes.DeleteLatency(userID, appName, nodeName)
}

func (d *Descheduler) handleSoftOnlyNode(userID string, appName string, nodeName string, latency *LatencyMeasurement, s int64) {
	if latency.Measurement <= s { // SOFT VALID NODE
		d.softValidNodes.AddLatency(userID, appName, nodeName, latency)
		d.hardValidNodes.DeleteLatency(userID, appName, nodeName)
	} else { // JUST HARD VALID NODE
		d.hardValidNodes.AddLatency(userID, appName, nodeName, latency)
		d.softValidNodes.DeleteLatency(userID, appName, nodeName)
	} // JUST HARD VALID NODE
	//d.invalidNodes.DeleteLatency(userID, appName, nodeName)
}

func (d *Descheduler) descheduleWorstHardValidNodes(N_tot int, userID, appName string) error {
	hardValidNodes := d.hardValidNodes.GetMeasurements()[userID][appName]
	sortedNodes := SortNodesByMeasurement(hardValidNodes)

	N_softValid := d.softValidNodes.GetTotalMeasurementsPerUserApp(userID, appName)
	N_hardValid := len(hardValidNodes)

	fmt.Println("softValidNodes: ", N_softValid, "\thardValidNodes: ", N_hardValid, "\ttotNodes: ", N_tot) //DEBUG
	for _, nodeName := range sortedNodes {
		if N_softValid+(N_hardValid-1) < N_tot/2 { //soft condition
			break
		}
		fmt.Println("The Soft Condition is valid, preceed descheudling the word HardValid Node...") //DEBUG
		if err := d.DescheduleAllPodsPerNode(appName, nodeName); err != nil {
			fmt.Printf("Error descheduling pods: %v\n", err)
			return err
		}
		d.latencyMeasurements.DeleteLatency(userID, appName, nodeName)
		d.hardValidNodes.DeleteLatency(userID, appName, nodeName)
		N_hardValid--
		fmt.Println("softValidNodes: ", N_softValid, "\thardValidNodes: ", N_hardValid, "\ttotNodes: ", N_tot) //DEBUG
	}
	fmt.Println("The Soft Condition is no longer valid!") //DEBUG
	return nil
}
