package main

import (
	"flag"
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type PauseSignal struct {
	isPaused bool
	appName  string
}

func main() {
	fmt.Println("Starting custom scheduler...")
	var kubeconfigPath string
	flag.StringVar(&kubeconfigPath, "kubeconfig", "", "Path to the kubeconfig file")
	flag.Parse()

	if kubeconfigPath == "" {
		fmt.Println("kubeconfig path must be specified")
		return
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		fmt.Println("Error building config:", err)
		return
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Println("Error creating clientset:", err)
		return
	}

	pauseDescheduler := make(chan PauseSignal)
	customScheduler := NewCustomScheduler(clientset, pauseDescheduler)
	latencyMeasurements := NewLatencyMeasurements()
	descheduler := NewDescheduler(clientset, latencyMeasurements, pauseDescheduler)

	var wg sync.WaitGroup
	wg.Add(2) // Aggiungi 2 al wait group per attendere entrambe le goroutine

	go func() {
		customScheduler.Run()
		wg.Done() // Decrementa il wait group quando la funzione termina
	}()

	go func() {
		descheduler.Run()
		wg.Done() // Decrementa il wait group quando la funzione termina
	}()

	wg.Wait() // Attendi che entrambe le goroutine siano terminate
}
