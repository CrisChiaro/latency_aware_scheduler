# latency_aware_scheduler
Latency-Aware Kubernetes Scheduling for Microservices Orchestration at the Edge

Questo esempio dimostra come creare un custom scheduler per Kubernetes che programma i Pods nei nodi in base alla latenza di rete tra l'utente che utilizzerà il servizio ed il nodo stesso.

## Descrizione dei file

### 1. latency-node-extender.yaml

Questo file definisce il Deployment per l'estensione del nodo che calcola la latenza di rete tra l'utente e il nodo. L'estensione del nodo viene eseguita come un singolo pod nel cluster. L'immagine Docker utilizzata in questo file deve essere compilata e caricata da te su un registro di container come Docker Hub o Google Container Registry.

### 2. latency-node-extender-service.yaml

Questo file definisce il servizio che espone l'estensione del nodo. Il servizio utilizza il selettore `app: latency-node-extender` per indirizzare il traffico verso il pod dell'estensione del nodo. Il servizio viene esposto sulla porta 8080.

### 3. main.go

Questo file contiene il codice Go per l'estensione del nodo. Calcola la latenza di rete tra l'utente e il nodo e assegna un punteggio ai nodi in base alla latenza. Il punteggio viene utilizzato dal custom scheduler per decidere quale nodo scegliere per l'allocazione dei Pods.

### 4. custom-scheduler.yaml

Questo file definisce la configurazione, il ServiceAccount, il ClusterRoleBinding e il Deployment per il custom scheduler. Il custom scheduler utilizza l'estensione del nodo per filtrare e dare priorità ai nodi in base alla latenza di rete.

## Istruzioni per l'uso

1. Compila l'estensione del nodo e crea un'immagine Docker per essa. Carica l'immagine su un registro di container come Docker Hub o Google Container Registry.
2. Aggiorna il campo `image` nel file `latency-node-extender.yaml` con il percorso dell'immagine Docker caricata.
3. Applica i file di configurazione al tuo cluster Kubernetes con i seguenti comandi:
* kubectl apply -f latency-node-extender.yaml
* kubectl apply -f latency-node-extender-service.yaml
* kubectl apply -f custom-scheduler.yaml


4. Quando crei nuovi Pods, specifica il campo `schedulerName` nella specifica del Pod per utilizzare il custom scheduler. Ad esempio:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-custom-scheduled-pod
spec:
  schedulerName: custom-scheduler
  containers:
  - name: my-container
    image: my-image
---
```

5. Il custom scheduler programmerà il Pod in base alla latenza di rete tra l'utente e i nodi del cluster.

