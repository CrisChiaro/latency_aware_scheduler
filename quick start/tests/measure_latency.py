import time
import requests
import argparse

def main(total_requests, interval, service_ip):
    service_url = f"http://{service_ip}:8080/?id=123"
    for i in range(total_requests):
        headers = {"X-Timestamp": str(int(time.time() * 1000))}
        start_time = time.time()
        response = requests.get(service_url, headers=headers)
        end_time = time.time()
        
        latency = (end_time - start_time) * 1000
        print(f"Richiesta {i+1} - Latenza di rete: {latency:.2f} millisecondi")
        
        time.sleep(interval)

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description='Misura la latenza di rete.')
    parser.add_argument('total_requests', type=int, help='Numero totale di richieste da effettuare')
    parser.add_argument('interval', type=int, help='Intervallo tra le richieste in secondi')
    parser.add_argument('service_ip', type=str, help='Indirizzo IP del servizio')
    args = parser.parse_args()
    
    main(args.total_requests, args.interval, args.service_ip)
