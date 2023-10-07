#!/bin/bash

if [ "$#" -ne 4 ]; then
    echo "Usage: $0 <total_number_of_requests> <URL> <interval> <success_threshold>"
    exit 1
fi

total_requests="$1"
service_url="http://$2:8080/?id=123"
interval="$3"
threshold="$4"
lat_base=21  # Average base latency
mediation_file="test_default_1.txt"

success_count=0  # Traccia il numero di test di successo

for ((j=1; j<=100; j++))
do
    # Esegui lo script start.sh all'inizio di ogni test
    ../start.sh

    echo "Inizio del test $j"

    total_latency=0
    valid_latencies=0

    for ((i=1; i<=$total_requests; i++))
    do
        start_time=$(date +%s%3N)
        http_code=$(curl -s -w '%{http_code}' -H "X-Timestamp: $(date +%s%3N)" -o /dev/null $service_url)
        end_time=$(date +%s%3N)

        lat_user=$((end_time - start_time))
        lat_adjusted=$((lat_user - (lat_user - lat_base) / 2))

        if [ $i -gt 1 ]; then
            # Se il codice di risposta è 200, considera la latenza come valida
            if [ "$http_code" -eq 200 ]; then
                total_latency=$((total_latency + lat_adjusted))
                valid_latencies=$((valid_latencies + 1))
             fi
        fi

        if (( i < total_requests )); then
            sleep "$interval"
        fi
    done

    avg_latency=$((total_latency / valid_latencies))
    echo "$avg_latency" >> $mediation_file

    # Verifica se il test è un successo
    if [ $avg_latency -lt $threshold ]; then
        success_count=$((success_count + 1))
    fi

    echo "Fine del test $j. La media della latenza è: $avg_latency ms"

    # Esegui lo script stop.sh alla fine di ogni test
    ../stop.sh
done

# Calcola e stampa il success rate
success_rate=$(bc <<< "scale=2; $success_count/100")
echo "Success rate: $success_rate"