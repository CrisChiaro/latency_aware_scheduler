package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Descheduler struct {
	clientset             *kubernetes.Clientset
	mutex                 *sync.Mutex
	latencyMeasurements   *LatencyMeasurements
	user_Cluster          *UserClusterAssociation
	invalidNodes          *LatencyMeasurements
	hardValidNodes        *LatencyMeasurements
	softValidNodes        *LatencyMeasurements
	hardLatencyThresholds *LatencyThresholds
	softLatencyThresholds *LatencyThresholds
	defaultReplicas       map[string]int32
}

func NewDescheduler(clientset *kubernetes.Clientset, mutex *sync.Mutex, latencyMeasurements *LatencyMeasurements, hardLatencyThresholds, softLatencyThresholds *LatencyThresholds) *Descheduler {
	return &Descheduler{
		clientset:             clientset,
		mutex:                 mutex,
		latencyMeasurements:   latencyMeasurements,
		user_Cluster:          NewUserClusterAssociation(),
		invalidNodes:          NewLatencyMeasurements(),
		hardValidNodes:        NewLatencyMeasurements(),
		softValidNodes:        NewLatencyMeasurements(),
		hardLatencyThresholds: hardLatencyThresholds,
		softLatencyThresholds: softLatencyThresholds,
		defaultReplicas:       make(map[string]int32),
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
		d.user_Cluster.CleanupAssociationsOlderThan(5) //REFRESH USERS-CLUSTERS ASSOCIATIONS
		d.latencyMeasurements.UpdateMeasurements(latencyMeasurements)
		d.latencyMeasurements.CleanupMeasurementsOlderThan(5)                                     //REFRESH MEASUREMENTS
		fmt.Printf("Current latency measurements: %v\n", d.latencyMeasurements.GetMeasurements()) //debug

		for appName, userMeasurements := range d.latencyMeasurements.GetMeasurements() {
			currentAppReplicas, ok := d.defaultReplicas[appName]
			if !ok {
				d.defaultReplicas[appName], err = d.getReplicasByApp(appName) //set the default replica number for the app if not exists
				if err != nil {
					fmt.Println(err)
				}
				currentAppReplicas = d.defaultReplicas[appName]
			}
			for userID, nodesMeasurements := range userMeasurements {

				fmt.Println("App: ", appName) //DEBUG
				fmt.Println("Current Replica set: ", currentAppReplicas, "\tDefault Replca set: ", d.defaultReplicas[appName])
				fmt.Println("User: ", userID) //DEBUG
				fmt.Println("Measurements: ") //DEBUG

				/*DEBUG*/
				for nodeName, nodeMeasurements := range nodesMeasurements {
					fmt.Println(nodeName, ": ", nodeMeasurements.Measurement) //DEBUG
				}
				err := d.descheduleInvalidNodes(appName, userID, nodesMeasurements)
				if err != nil {
					fmt.Printf("Error descheduling pods in the InvalideNodes: %v\n", err)
				}
				_, needSoftCondition := d.softLatencyThresholds.GetLatency(appName)
				fmt.Println("Soft Condition to be checked: ", needSoftCondition) //DEBUG
				if needSoftCondition /*&& N_misured == N_tot*/ {                 //Se ho una soft contraint: CICLO FINALE per i soft nodes
					fmt.Println("Checking the Soft Condition...") //DEBUG
					d.descheduleWorstHardValidNodes(N_tot, appName, userID)
				}
				fmt.Println() //DEBUG
			}
			//SEND INFORMATION TO THE CUSTOM LOAD BALANCER()
			if d.AllPodsAssigned(appName) {
				fmt.Println("All pods assigned to users, increasing the replica sets...") //DEBUG
				err := d.increaseReplicas(appName)
				if err != nil {
					fmt.Printf("Error increasing replicas for app %s: %v\n", appName, err)
				}
			} else {
				fmt.Print("NOT INCREASING THE REPLICA SET.\n\n") //DEBUG
			}

			if currentAppReplicas > d.defaultReplicas[appName] { //if there are too Replicas, I check if I need to deschedule some Pods
				d.descheduleUnassociatedPods(appName, d.user_Cluster, &currentAppReplicas)
			}
		}
		if d.user_Cluster.changed {
			fmt.Printf("SENDING THE ASSOCIATION DATA TO ROUTING MANAGER:\n")
			err = d.sendAssociationsToRoutingManager(d.user_Cluster)
			if err != nil {
				fmt.Println(err.Error())
			}
			d.user_Cluster.changed = false
		} else {
			fmt.Printf("ASSOCIATION DATA DIDN'T CHANGED\n")
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
		if strings.HasPrefix(pod.Namespace, "kube") || strings.HasPrefix(pod.Namespace, "routing") || strings.HasPrefix(pod.Namespace, "liqo") || strings.HasPrefix(pod.Namespace, "metallb") || strings.HasPrefix(pod.Namespace, "local") || len(pod.Status.PodIP) == 0 { //scarto i pod di sistema o non ancora schedulati
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
			if _, exists := measurements[appName]; !exists {
				measurements[appName] = make(map[string]map[string]*LatencyMeasurement)
			}
			if _, exists := measurements[appName][userID]; !exists {
				measurements[appName][userID] = make(map[string]*LatencyMeasurement)
			}
			// Check if the measurement map for this user and app is nil, if it is, initialize it
			if measurements[appName][userID] == nil {
				measurements[appName][userID] = make(map[string]*LatencyMeasurement)
			}

			if measurements[appName][userID][pod.Spec.NodeName] == nil || measurements[appName][userID][pod.Spec.NodeName].Timestamp.Before(userMeasurements.Timestamp) {
				// if there was already a measurement, i check the timestamp
				measurements[appName][userID][pod.Spec.NodeName] = userMeasurements
			}
		}
	}

	return measurements, nil
}

func (d *Descheduler) DescheduleAllPodsPerNode(appName, userID, nodeName string) (int, error) {
	descheduledPods := 0
	// Get the list of pods on the worst performing node
	pods, err := d.clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})

	if err != nil {
		return descheduledPods, err
	}

	for _, pod := range pods.Items {
		// Add any necessary filters here, e.g., by labels or namespace
		if pod.Namespace == "kube-system" || strings.HasPrefix(pod.Namespace, "routing") || strings.HasPrefix(pod.Namespace, "liqo") || strings.HasPrefix(pod.Namespace, "metallb") || strings.HasPrefix(pod.Namespace, "local") || len(pod.Status.PodIP) == 0 { //scarto i pod di sistema o non ancora schedulati
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
		needContinue := false
		//Check if another user is associated to this pod
		userClusterAssociations := d.user_Cluster.GetUserClusterAssociations()
		for _, appAssociation := range userClusterAssociations { //check in all userAssosiactions if there is the pod
			clusterMeasure := appAssociation[appName]
			if clusterMeasure != nil && clusterMeasure.PodName == pod.Name {
				fmt.Println("The pod ", pod.Name, " is associated to a user. So undeschedulable for now.") //DEBUG
				needContinue = true
				break
			}
		}
		if needContinue {
			continue
		}

		err = d.clientset.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
		if err != nil {
			// Log error and continue with next pod
			fmt.Println("Failed to delete pod", pod.Name, "with error", err.Error())
			continue
		}
		descheduledPods++
		fmt.Println("Successfully deleted pod", pod.Name)
	}
	return descheduledPods, nil
}

func (d *Descheduler) getTotalNodes() (int, error) {
	nodes, err := d.clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return -1, err
	}
	return len(nodes.Items) - 1, nil
}

func (d *Descheduler) descheduleInvalidNodes(appName, userID string, nodesMeasurements map[string]*LatencyMeasurement) error {
	h, hExists := d.hardLatencyThresholds.GetLatency(appName)
	s, sExists := d.softLatencyThresholds.GetLatency(appName)

	for nodeName, latency := range nodesMeasurements {
		if hExists && latency.Measurement <= h { //hard valid node
			d.handleValidNode(appName, userID, nodeName, latency, s, sExists)
		} else if hExists { //invalid node
			d.handleInvalidNode(appName, userID, nodeName, latency)
		} else if sExists { //hard contraint not exists
			d.handleSoftOnlyNode(appName, userID, nodeName, latency, s)
		} else {
			fmt.Println("Both Soft and Hard contraint are not present!!!") //TODO: ERROR
		}
	}
	return nil
}

func (d *Descheduler) handleValidNode(appName, userID string, nodeName string, latency *LatencyMeasurement, s int64, sExists bool) {
	d.invalidNodes.DeleteLatency(appName, userID, nodeName)

	if sExists { // Check if exists soft constraint
		if latency.Measurement <= s { // SOFT valid node
			d.softValidNodes.AddLatency(appName, userID, nodeName, latency)
			d.hardValidNodes.DeleteLatency(appName, userID, nodeName)
			d.user_Cluster.AddAssociation(userID, appName, nodeName, latency, true)
		} else { // JUST HARD valid node
			d.hardValidNodes.AddLatency(appName, userID, nodeName, latency)
			d.softValidNodes.DeleteLatency(appName, userID, nodeName)
			d.user_Cluster.AddAssociation(userID, appName, nodeName, latency, false)
		}
		//d.invalidNodes.DeleteLatency(userID, appName, nodeName)
	} else { // JUST HARD valid node
		d.hardValidNodes.AddLatency(appName, userID, nodeName, latency)
		d.user_Cluster.AddAssociation(userID, appName, nodeName, latency, false)
	}
}

func (d *Descheduler) handleInvalidNode(appName, userID string, nodeName string, latency *LatencyMeasurement) {
	fmt.Println(nodeName, " is an invalid node for the user: ", userID)
	//d.invalidNodes.AddLatency(userID, appName, nodeName, latency)
	d.latencyMeasurements.DeleteLatency(appName, userID, nodeName)
	d.hardValidNodes.DeleteLatency(appName, userID, nodeName)
	d.softValidNodes.DeleteLatency(appName, userID, nodeName)
	d.user_Cluster.RemoveUserClusterAssiciation(userID, appName)
	d.DescheduleAllPodsPerNode(appName, userID, nodeName)
}

func (d *Descheduler) handleSoftOnlyNode(appName, userID string, nodeName string, latency *LatencyMeasurement, s int64) {
	if latency.Measurement <= s { // SOFT VALID NODE
		d.softValidNodes.AddLatency(appName, userID, nodeName, latency)
		d.hardValidNodes.DeleteLatency(appName, userID, nodeName)
		d.user_Cluster.AddAssociation(userID, appName, nodeName, latency, true) //if it exists, I substitute it because a soft costraint is more strict
	} else { // JUST HARD VALID NODE
		d.hardValidNodes.AddLatency(appName, userID, nodeName, latency)
		d.softValidNodes.DeleteLatency(appName, userID, nodeName)
		d.user_Cluster.AddAssociation(userID, appName, nodeName, latency, false)

	} // JUST HARD VALID NODE
	//d.invalidNodes.DeleteLatency(userID, appName, nodeName)
}

func (d *Descheduler) descheduleWorstHardValidNodes(N_tot int, appName, userID string) error {
	hardValidNodes := d.hardValidNodes.GetMeasurements()[appName][userID]
	sortedNodes := SortNodesByMeasurement(hardValidNodes)

	N_softValid := d.softValidNodes.GetTotalMeasurementsPerUserApp(appName, userID)
	N_hardValid := len(hardValidNodes)

	fmt.Println("softValidNodes: ", N_softValid, "\thardValidNodes: ", N_hardValid, "\ttotNodes: ", N_tot) //DEBUG
	for _, nodeName := range sortedNodes {
		if N_softValid+(N_hardValid-1) < N_tot/2 { //soft condition
			d.user_Cluster.AddAssociation(userID, appName, nodeName, d.hardValidNodes.data[appName][userID][nodeName], false) // se non esiste, l'ho eliminato precedentemente
			break
		}
		fmt.Println("The Soft Condition is valid, preceed descheudling the word HardValid Node...") //DEBUG
		if a, exists := d.user_Cluster.GetUserClusterAssociation(userID, appName); exists {         //elimino l'associazione poichè ce n'è una migliore
			if a.ClusterName == nodeName {
				d.user_Cluster.RemoveUserClusterAssiciation(userID, appName)
			}
		}
		if _, err := d.DescheduleAllPodsPerNode(appName, userID, nodeName); err != nil {
			fmt.Printf("Error descheduling pods: %v\n", err)
			return err
		}
		d.latencyMeasurements.DeleteLatency(appName, userID, nodeName)
		d.hardValidNodes.DeleteLatency(appName, userID, nodeName)
		N_hardValid--
		fmt.Println("softValidNodes: ", N_softValid, "\thardValidNodes: ", N_hardValid, "\ttotNodes: ", N_tot) //DEBUG
	}
	//l'associazione è sicuramente settata poichè:
	// - se esco con il break, la setto eventualmente in quell'if
	// - se no esiste sicuramente almeno un soft constraint valid node, e quindi settata precedentemente
	fmt.Println("The Soft Condition is not (or no longer) valid!") //DEBUG
	return nil
}

func (d *Descheduler) AllPodsAssigned(appName string) bool {
	time.Sleep(3 * time.Second) //need to wait the new potential scheduling
	d.mutex.Lock()
	defer d.mutex.Unlock()

	allPods, err := d.clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Error listing pods: %v\n", err)
		return false
	}
	for _, pod := range allPods.Items {
		// Add any necessary filters here, e.g., by labels or namespace
		if strings.HasPrefix(pod.Namespace, "kube") || strings.HasPrefix(pod.Namespace, "routing") || strings.HasPrefix(pod.Namespace, "liqo") || strings.HasPrefix(pod.Namespace, "metallb") || strings.HasPrefix(pod.Namespace, "local") || len(pod.Status.PodIP) == 0 { //scarto i pod di sistema o non ancora schedulati
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
		userClusterAssociations := d.user_Cluster.GetUserClusterAssociations()
		podFoundInAssociations := false
		for _, appAssociation := range userClusterAssociations { //check in all userAssosiactions if there is the pod
			clusterMeasure := appAssociation[appName]
			if clusterMeasure != nil && clusterMeasure.PodName == pod.Name {
				podFoundInAssociations = true
				break
			}
		}
		if !podFoundInAssociations {
			fmt.Println("\nThe pod ", pod.Name, " is not associated to any user.") //DEBUG
			return false
		}
	}
	return true
}

func (d *Descheduler) increaseReplicas(appName string) error {
	deployment, err := d.clientset.AppsV1().Deployments("default").Get(context.Background(), appName+"-deployment", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("Error retrieving deployment: %v", err)
	}
	*deployment.Spec.Replicas++
	_, err = d.clientset.AppsV1().Deployments("default").Update(context.Background(), deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("Error updating deployment: %v", err)
	}
	return nil
}

func (d *Descheduler) getReplicasByApp(appName string) (int32, error) {
	deployment, err := d.clientset.AppsV1().Deployments("default").Get(context.Background(), appName+"-deployment", metav1.GetOptions{})
	if err != nil {
		return -1, fmt.Errorf("Error retrieving deployment: %v", err)
	}
	return *deployment.Spec.Replicas, nil
}

func (d *Descheduler) descheduleUnassociatedPods(appName string, uca *UserClusterAssociation, currentReplicas *int32) error {
	// Get all pods for the given appName
	pods, err := d.clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", appName),
	})
	if err != nil {
		return fmt.Errorf("error retrieving pods: %v", err)
	}

	unassociatedPods := []*v1.Pod{}

	// Check each pod if it's associated
	for _, pod := range pods.Items {
		podAssociated := false
		for _, userAssociations := range uca.GetUserClusterAssociations() {
			clusterInfo := userAssociations[appName]
			if clusterInfo.PodName == pod.Name {
				podAssociated = true
				break
			}
			if podAssociated {
				break
			}
		}
		// If pod is not associated, add it to the list of unassociated pods
		if !podAssociated {
			unassociatedPods = append(unassociatedPods, &pod)
		}
	}

	// If there are more than one unassociated pods, delete all but one
	if len(unassociatedPods) > 1 {
		for _, pod := range unassociatedPods[1:] { // keep the first one, delete the rest
			// Only deschedule pods and decrease the replicas count if the current replicas count is more than the default
			if *currentReplicas <= d.defaultReplicas[appName] {
				return nil
			}

			deployment, err := d.clientset.AppsV1().Deployments("default").Get(context.Background(), appName+"-deployment", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("error retrieving deployment: %v", err)
			}

			// Pause the deployment
			deployment.Spec.Paused = true
			_, err = d.clientset.AppsV1().Deployments("default").Update(context.Background(), deployment, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("error pausing deployment: %v", err)
			}

			// Decrease the replicas count
			*deployment.Spec.Replicas--
			_, err = d.clientset.AppsV1().Deployments("default").Update(context.Background(), deployment, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("error decreasing deployment replicas: %v", err)
			}

			// Decrease the current replicas count
			*currentReplicas--

			// Delete the unassociated pod
			err = d.clientset.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf("error deleting pod %s: %v", pod.Name, err)
			}

			// Unpause the deployment
			deployment.Spec.Paused = false
			_, err = d.clientset.AppsV1().Deployments("default").Update(context.Background(), deployment, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("error unpausing deployment: %v", err)
			}
		}
	}

	return nil
}

func (d *Descheduler) sendAssociationsToRoutingManager(associations *UserClusterAssociation) error {
	routing_manager_service_IP := "10.11.167.5"
	endpoint := fmt.Sprintf("http://%s/update-associations", routing_manager_service_IP)

	// Converti le associazioni dell'utente in JSON
	jsonData, err := json.Marshal(associations.GetUserClusterAssociations())
	if err != nil {
		return fmt.Errorf("Error marshaling associations: %v", err)
	}

	// Stampa il JSON per scopi di debug
	fmt.Printf("Sending the following JSON data to the routing manager: %s\n", string(jsonData))

	// Esegui la richiesta POST al routing manager
	resp, err := http.Post(endpoint, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("Error sending associations to routing-manager: %v", err)
	}
	defer resp.Body.Close()

	// Verifica lo stato della risposta
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		// Stampa il corpo della risposta se lo stato non è OK per scopi di debug
		fmt.Printf("Routing manager response status: %s, body: %s\n", resp.Status, string(body))
		return fmt.Errorf("Failed to send associations, status: %s, body: %s", resp.Status, body)
	}

	// Se tutto va bene, stampa un messaggio di successo
	fmt.Println("Associations successfully sent to routing manager")
	return nil
}
