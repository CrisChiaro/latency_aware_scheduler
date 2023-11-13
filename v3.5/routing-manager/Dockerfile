# Utilizza l'ultima versione di Golang per il builder
FROM golang:latest AS builder

# Imposta la directory di lavoro nel container
WORKDIR /app

# Copia i file sorgente nell'immagine
COPY . .

# Scarica le dipendenze
RUN go mod download

# Compila l'applicazione
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o routing-manager

# Fase dell'immagine finale, utilizza Alpine per un'immagine piccola e sicura
FROM alpine:latest

# Installa i certificati necessari per le chiamate HTTPS
RUN apk --no-cache add ca-certificates

# Imposta la directory di lavoro nel container
WORKDIR /root/

# Copia l'eseguibile compilato dall'immagine del builder
COPY --from=builder /app/routing-manager .

# Esegui l'applicazione quando il container viene avviato
CMD ["./routing-manager"]
