#!/bin/bash

if [ "$#" -ne 3 ]; then
    echo "Utilizzo: $0 <numero_totale_richieste> <indirizzo_IP_servizio> <intervallo_tra_le_richieste_in_secondi>"
    exit 1
fi

total_requests="$1"
service_url="http://$2:8080/?id=123"
interval="$3"

for ((i=1; i<=$total_requests; i++))
do
    start_time=$(date +%s%3N)
    response=$(curl -w '%{http_code}' -H "X-Timestamp: $(date +%s%3N)" -s -o /dev/null "$service_url")
    end_time=$(date +%s%3N)

    latency=$((end_time - start_time))
    echo "Richiesta $i - Latenza di rete: $latency millisecondi - HTTP Status: $response"

    if (( i < total_requests )); then
        sleep "$interval"
    fi
done
