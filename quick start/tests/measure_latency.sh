#!/bin/bash

# Verifica che siano stati passati tre argomenti
if [ "$#" -ne 3 ]; then
    echo "Utilizzo: $0 <numero_totale_richieste> <intervallo_tra_le_richieste_in_secondi> <indirizzo_IP_servizio>"
    exit 1
fi

# Numero totale di richieste da effettuare (passato come primo argomento)
total_requests="$1"

# Intervallo tra le richieste in secondi (passato come terzo argomento)
interval="$2"

# URL del servizio (passato come secondo argomento)
service_url="http://$3:8080/?id=123"

# Esegui le richieste
for ((i=1; i<=$total_requests; i++))
do
    # Esegui la richiesta con curl e misura il tempo
    start_time=$(date +%s%3N)
    response=$(curl -H "X-Timestamp: $(date +%s%3N)" -s "$service_url")
    end_time=$(date +%s%3N)

    # Calcola la latenza
    latency=$((end_time - start_time))

    # Stampa la latenza
    echo "Richiesta $i - Latenza di rete: $latency millisecondi"

    # Attendi per l'intervallo specificato tra le richieste
    sleep "$interval"
done