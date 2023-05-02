package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"
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
				Score: int(50 - latency), // Assumiamo un limite di latenza di 50 ms
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
	return 0, nil
}

func main() {
	router := mux.NewRouter()

	latencyExtender := &LatencyExtender{}

	router.HandleFunc("/filter", latencyExtender.Filter).Methods("POST")
	router.HandleFunc("/prioritize", latencyExtender.Prioritize).Methods("POST")

	port := 8080
	fmt.Printf("Starting server on port %d...\n", port)
	http.ListenAndServe(fmt.Sprintf(":%d", port), router)
}
