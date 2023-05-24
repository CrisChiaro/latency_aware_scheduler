package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var appAddress string

type LatencyMeasurement struct {
	PodNamespace string
	NodeName     string
	Measurement  int64
	Timestamp    time.Time
}

type LatencyMeasurements struct {
	data map[string]*LatencyMeasurement
} //per ogni utente ho una una misura!

func NewLatencyMeasurements() *LatencyMeasurements {
	return &LatencyMeasurements{
		data: make(map[string]*LatencyMeasurement),
	}
}

func (l *LatencyMeasurements) AddLatency(userID string, measurement *LatencyMeasurement) {
	l.data[userID] = measurement
}

func latencyMiddleware(next http.Handler, pod *v1.Pod, latencyMeasurements *LatencyMeasurements) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("Misuring latency...") //DEBUG
		start := time.Now()
		next.ServeHTTP(w, r)
		latency := time.Since(start).Milliseconds()

		//TEMPORANEO: USO L'IP COME IDENTIFICATIVO
		host, _, err := net.SplitHostPort(r.RemoteAddr) // Estrai l'indirizzo IP del client dalla richiesta
		if err != nil {
			fmt.Printf("Error splitting host and port from RemoteAddr: %v\n", err)
			return
		}
		userID := host // Utilizza l'indirizzo IP del client come identificativo dell'utente
		// Estrai l'indirizzo IP del client dalla richiesta                                                             // Utilizza l'indirizzo IP del client come identificativo dell'utente
		fmt.Println("Latency calculated!\tlatency: ", latency, "\tuserID(IP): ", userID, "\tPodNamespace: ", pod.Namespace) //DEBUG

		latencyMeasurements.AddLatency(userID, &LatencyMeasurement{
			PodNamespace: pod.Namespace,
			NodeName:     pod.Spec.NodeName,
			Measurement:  latency,
			Timestamp:    time.Now(),
		})
	})
}

func getCurrentPod(clientset *kubernetes.Clientset) (*v1.Pod, error) {
	namespace := os.Getenv("POD_NAMESPACE")
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		return nil, fmt.Errorf("Error getting current pod: POD_NAME environment variable is not set")
	}
	if namespace == "" {
		return nil, fmt.Errorf("Error getting current pod: POD_NAMESPACE environment variable is not set")
	}

	pod, err := clientset.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error getting current pod: %v", err)
	}

	return pod, nil
}

func measureLatency(clientset *kubernetes.Clientset, latencyMeasurements *LatencyMeasurements) {
	pod, err := getCurrentPod(clientset)
	if err != nil {
		fmt.Printf("Error getting current pod: %v\n", err)
		return
	}

	appAddress = "http://localhost:80"
	fmt.Println("Pod Name: ", pod.Name, "\tNamespace: ", pod.Namespace, "\tIP: ", appAddress) //DEBUG
	mux := http.NewServeMux()

	mux.Handle("/", latencyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/measurements" {
			fmt.Println("Request to the app") //DEBUG
			// Proxy the request to the application
			proxyURL, _ := url.Parse(appAddress)
			proxy := httputil.NewSingleHostReverseProxy(proxyURL)
			proxy.ServeHTTP(w, r)
		}
	}), pod, latencyMeasurements))

	mux.HandleFunc("/measurements", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("/measurements contacted") //DEBUG

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		latencyMeasurementsJSON, err := json.Marshal(latencyMeasurements.Data())
		if err != nil {
			http.Error(w, "Error marshaling latency measurements", http.StatusInternalServerError)
			return
		}

		w.Write(latencyMeasurementsJSON)
		latencyMeasurements.Reset()
	})

	http.ListenAndServe(":8080", mux)
}

func (l *LatencyMeasurements) Data() map[string]*LatencyMeasurement {
	return l.data
}

func main() {
	fmt.Println("Latency Meter started")

	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			fmt.Println("Error in getting Kubernetes configuration:", err)
			return
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Println("Error in creating Kubernetes clientset:", err)
		return
	}

	latencyMeasurements := NewLatencyMeasurements()
	go measureLatency(clientset, latencyMeasurements)

	select {}
}

func (l *LatencyMeasurements) Reset() {
	l.data = make(map[string]*LatencyMeasurement)
}
