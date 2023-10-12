#!/bin/bash

if [ "$#" -ne 5 ]; then
    echo "Usage: $0 <total_number_of_requests> <URL> <interval> <success_threshold> <text_file>"
    exit 1
fi

total_requests="$1"
service_url="http://$2:8080/?id=123"
interval="$3"
threshold="$4"
mediation_file="$5"
lat_base=21


total_avg_latency=0
total_conv_time=0

for ((j=1; j<=100; j++))
do
    # Esegui lo script start.sh all'inizio di ogni test
    ./start.sh

    echo "Inizio del test $j"

    total_latency=0
    valid_latencies=0
    start_test_time=$(date +%s%3N)  # Tempi iniziali del test
    t_conv=0

    consecutive_below_threshold=0
    for ((i=1; i<=$total_requests; i++))
    do
        current_time=$(date +%s%3N)
        http_code=$(curl -s -w '%{http_code}' -H "X-Timestamp: $(date +%s%3N)" -o /dev/null $service_url)
        end_time=$(date +%s%3N)

        lat_user=$((end_time - current_time))
        lat_adjusted=$((lat_user - (lat_user - lat_base) / 2))

        # Se il codice di risposta è 200, considera la latenza come valida
        if [ "$http_code" -eq 200 ]; then
            total_latency=$((total_latency + lat_adjusted))
            valid_latencies=$((valid_latencies + 1))

            if [ $lat_adjusted -ge $threshold ]; then
                t_conv=$((current_time - start_test_time))
                consecutive_below_threshold=0  # Resetta il conteggio
            else
                consecutive_below_threshold=$((consecutive_below_threshold + 1))
                if [ $consecutive_below_threshold -ge 3 ]; then
                    break  # Interrompi il loop
                fi
            fi
        fi

        if (( i < total_requests )); then
            sleep "$interval"
        fi
    done

    avg_latency=$((total_latency / valid_latencies))
    echo "$avg_latency" >> $mediation_file

    total_avg_latency=$((total_avg_latency + avg_latency))
    total_conv_time=$((total_conv_time + t_conv))

    echo "Fine del test $j. La media della latenza è: $avg_latency ms"
    echo "Tempo di convergenza per il test $j: $t_conv ms"
    echo "$j lat_mean: $avg_latency ms" >> $mediation_file
    echo "$j t_conv: $t_conv ms" >> $mediation_file


    # Esegui lo script stop.sh alla fine di ogni test
    ./stop.sh
done

# Calcola e stampa la latenza media e il tempo di convergenza medio
avg_total_latency=$((total_avg_latency / 100))
avg_conv_time=$((total_conv_time / 100))

echo "Latenza media su tutti i test: $avg_total_latency ms"
echo "Tempo di convergenza medio su tutti i test: $avg_conv_time ms"
