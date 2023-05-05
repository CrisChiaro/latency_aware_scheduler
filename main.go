package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	v1 "k8s.io/api/core/v1"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
)

type LatencyExtender struct{}

func (e *LatencyExtender) Filter(args schedulerapi.ExtenderArgs) *schedulerapi.ExtenderFilterResult {
	var filteredNodes []v1.Node
	failedNodes := make(schedulerapi.FailedNodesMap)

	for _, node := range args.Nodes.Items {
		latency, err := getLatencyForNode(node.Name)
		if err == nil && latency < 50 { // Assumiamo un limite di latenza di 50 ms
			filteredNodes = append(filteredNodes, node)
		} else {
			failedNodes[node.Name] = fmt.Sprintf("Latency too high: %v", err)
		}
	}

	result := schedulerapi.ExtenderFilterResult{
		Nodes: &v1.NodeList{
			Items: filteredNodes,
		},
		FailedNodes: failedNodes,
	}

	return &result
}

func (e *LatencyExtender) Prioritize(args schedulerapi.ExtenderArgs) *schedulerapi.HostPriorityList {
	priorities := make(schedulerapi.HostPriorityList, len(args.Nodes.Items))

	for i, node := range args.Nodes.Items {
		latency, err := getLatencyForNode(node.Name)
		if err == nil {
			priorities[i] = schedulerapi.HostPriority{
				Host:  node.Name,
				Score: int64(50 - latency), // Assumiamo un limite di latenza di 50 ms
			}
		} else {
			priorities[i] = schedulerapi.HostPriority{
				Host:  node.Name,
				Score: 0,
			}
		}
	}

	return &priorities
}

func getLatencyForNode(nodeName string) (int, error) {
	// Implementare la logica per ottenere la latenza di rete per il nodo
	resp, err := http.Get(fmt.Sprintf("http://%s:8080/latency", nodeName))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	latency, err := strconv.Atoi(string(body))
	if err != nil {
		return 0, err
	}

	return latency, nil
}

func main() {
	router := mux.NewRouter()

	latencyExtender := &LatencyExtender{}

	router.HandleFunc("/filter", func(w http.ResponseWriter, r *http.Request) {
		var args schedulerapi.ExtenderArgs
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result := latencyExtender.Filter(args)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}).Methods("POST")

	router.HandleFunc("/prioritize", func(w http.ResponseWriter, r *http.Request) {
		var args schedulerapi.ExtenderArgs
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result := latencyExtender.Prioritize(args)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}).Methods("POST")

	port := 8080
	fmt.Printf("Starting server on port %d...\n", port)
	http.ListenAndServe(fmt.Sprintf(":%d", port), router)
}
