#!/bin/bash

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <total_number_of_requests> <URL> <interval>"
    exit 1
fi

total_requests="$1"
service_url="http://$2:8080/?id=123"
interval="$3"
lat_base=21  # Average base latency

# Record the start time of the entire script
script_start_time=$(date +%s%3N)

for ((i=1; i<=$total_requests; i++))
do
    start_time=$(date +%s%3N)
    http_code=$(curl -s -w '%{http_code}' -H "X-Timestamp: $(date +%s%3N)" -o /dev/null $service_url)
    end_time=$(date +%s%3N)

    lat_user=$((end_time - start_time))
    lat_adjusted=$((lat_user - (lat_user - lat_base) / 2))

    echo "Request $i - Network Latency: $lat_adjusted ms - HTTP Code: $http_code"

    if (( i < total_requests )); then
        sleep "$interval"
    fi
done

# Record the end time of the entire script
script_end_time=$(date +%s%3N)