package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type LatencyMeasurement struct {
	PodNamespace string
	PodName      string
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
func (l *LatencyMeasurements) Data() map[string]*LatencyMeasurement {
	return l.data
}

func (l *LatencyMeasurements) AddLatency(userID string, measurement *LatencyMeasurement) {
	l.data[userID] = measurement
}

func (l *LatencyMeasurements) Reset() {
	l.data = make(map[string]*LatencyMeasurement)
}

func getCurrentPod(clientset *kubernetes.Clientset) (*v1.Pod, error) {
	//namespace := os.Getenv("POD_NAMESPACE")
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		return nil, fmt.Errorf("Error getting current pod: POD_NAME environment variable is not set")
	}

	/*
		if namespace == "" {
			return nil, fmt.Errorf("Error getting current pod: POD_NAMESPACE environment variable is not set")
		}
	*/

	/* START DEBUG
	fmt.Println("POD_NAMESPACE: ", namespace, "\tPOD_NAME: ", podName) //DEBUG
	pods, err := clientset.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error getting pod list for this namespace: %v", err)
	}
	fmt.Println("All pods in ", namespace, ":") //DEBUG
	for i, pod := range pods.Items {
		fmt.Println(i, ") ", pod.Name) //DEBUG
	}

	pods, err = clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error getting pod list: %v", err)
	}
	fmt.Println("All pods :") //DEBUG
	for i, pod := range pods.Items {
		fmt.Println(i, ") ", pod.Name) //DEBUG
	}
	  END DEBUG */

	//uso il namespace default per semplificare il problema, altrimenti dovrei gestire tutto il passaggio del corretto kubeconfig per cluster!!
	pod, err := clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})

	if err != nil {
		return nil, err
	}

	return pod, nil
}

/*
func LatencyCalculated(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
*/

func latencyMiddleware(next http.Handler, pod *v1.Pod, latencyMeasurements *LatencyMeasurements) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		userID := r.URL.Query().Get("id")                 // Extract the userID from the query parameters
		clientTimestampStr := r.Header.Get("X-Timestamp") // Extract the user timestamp from the header

		// Convert the latency to an integer
		clientTimestamp, err := strconv.ParseInt(clientTimestampStr, 10, 64)
		if err != nil {
			fmt.Println("Error parsing client timestamp. \tuserID: ", userID, "\tClientTimestamp: ", clientTimestampStr) //DEBUG
		} else {
			serverTimestamp := time.Now().UnixNano() / int64(time.Millisecond) // Current time in milliseconds
			latency := serverTimestamp - clientTimestamp
			fmt.Println("Latency calculated!\tlatency: ", latency, "\tuserID(IP): ", userID, "\tPodNamespace: ", pod.Namespace) //DEBUG
			latencyMeasurements.AddLatency(userID, &LatencyMeasurement{
				PodNamespace: pod.Namespace,
				PodName:      pod.Name,
				Measurement:  latency,
				Timestamp:    time.Now(),
			})
		}

		fmt.Println("Request to the app") //DEBUG
		next.ServeHTTP(w, r)
	})
}

func measureLatency(clientset *kubernetes.Clientset, latencyMeasurements *LatencyMeasurements) {
	pod, err := getCurrentPod(clientset)
	if err != nil {
		fmt.Printf("Error getting current pod: %v\n", err)
		return
	}
	appAddress := "http://localhost:80"
	fmt.Println("Pod Name: ", pod.Name, "\tNamespace: ", pod.Namespace, "\tIP: ", appAddress) //DEBUG

	router := mux.NewRouter()

	headers := handlers.AllowedHeaders([]string{"X-Requested-With", "Content-Type", "Authorization", "X-Timestamp"})
	methods := handlers.AllowedMethods([]string{"GET", "POST", "PUT", "DELETE", "OPTIONS"})
	origins := handlers.AllowedOrigins([]string{"*"})

	router.Handle("/", latencyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/measurements" && r.URL.Path != "/latency" {
			// Proxy the request to the application
			proxyURL, _ := url.Parse(appAddress)
			proxy := httputil.NewSingleHostReverseProxy(proxyURL)
			proxy.ServeHTTP(w, r)
		}
	}), pod, latencyMeasurements)).Methods("GET")
	//router.HandleFunc("/latency", LatencyCalculated).Methods("GET") //anotherway to mesure latency
	router.HandleFunc("/measurements", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("/measurements contacted (IP: ", r.RemoteAddr, ")") //DEBUG

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		latencyMeasurementsJSON, err := json.Marshal(latencyMeasurements.Data())
		if err != nil {
			http.Error(w, "Error marshaling latency measurements", http.StatusInternalServerError)
			return
		}

		w.Write(latencyMeasurementsJSON)
		latencyMeasurements.Reset()
	}).Methods("GET")

	http.ListenAndServe(":8080", handlers.CORS(headers, methods, origins)(router))
}

func main() {
	fmt.Println("Latency Meter started and it's listening at port 8080...")

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
	go measureLatency(clientset, latencyMeasurements) //nuova go routine

	select {} //mantiene il programma in esecuzione
}
