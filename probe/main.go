package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	writeAttempts = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "pg_write_attempts_total",
			Help: "Total number of write attempts",
		},
	)
	writeErrors = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "pg_write_errors_total",
			Help: "Total number of failed writes",
		},
	)
	failoverDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "pg_failover_duration_seconds",
			Help:    "Time taken for writes to recover after a failure",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0, 60.0, 120.0},
		},
	)
)

func init() {
	prometheus.MustRegister(writeAttempts)
	prometheus.MustRegister(writeErrors)
	prometheus.MustRegister(failoverDuration)
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getConnStr() string {
	host := getEnv("DB_HOST", "postgres")
	port := getEnv("DB_PORT", "5432")
	user := getEnv("DB_USER", "postgres")
	pass := getEnv("DB_PASS", "SuperSecretPassword123")
	dbName := getEnv("DB_NAME", "postgres")

	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=3", host, port, user, pass, dbName)
}

func setupDB() bool {
	db, err := sql.Open("postgres", getConnStr())
	if err != nil {
		log.Printf("Failed to open DB connection: %v\n", err)
		return false
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS ha_probe_metrics (
			id SERIAL PRIMARY KEY,
			ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Printf("Failed to create table: %v\n", err)
		return false
	}

	log.Println("Database setup complete.")
	return true
}

func main() {
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Println("Prometheus metrics server started on port 8000")
		log.Fatal(http.ListenAndServe(":8000", nil))
	}()

	for !setupDB() {
		log.Println("Retrying database setup in 5 seconds...")
		time.Sleep(5 * time.Second)
	}

	pollIntervalStr := getEnv("POLL_INTERVAL", "1.0")
	pollIntervalFloat, err := strconv.ParseFloat(pollIntervalStr, 64)
	if err != nil {
		pollIntervalFloat = 1.0
	}
	pollInterval := time.Duration(pollIntervalFloat * float64(time.Second))

	var outageStart time.Time
	isOutage := false

	for {
		writeAttempts.Inc()

		db, err := sql.Open("postgres", getConnStr())
		if err == nil {
			_, err = db.Exec("INSERT INTO ha_probe_metrics (ts) VALUES (NOW())")
			db.Close()
		}

		if err != nil {
			writeErrors.Inc()
			if !isOutage {
				isOutage = true
				outageStart = time.Now()
				log.Printf("Write failed. Outage started: %v\n", err)
			}
		} else {
			if isOutage {
				duration := time.Since(outageStart).Seconds()
				failoverDuration.Observe(duration)
				log.Printf("Recovered from outage. Failover duration: %.2f seconds\n", duration)
				isOutage = false
			}
		}

		time.Sleep(pollInterval)
	}
}
