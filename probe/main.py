import os
import time
import psycopg2
import logging
from prometheus_client import start_http_server, Counter, Histogram

logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

# Prometheus Metrics
WRITE_ATTEMPTS = Counter('pg_write_attempts_total', 'Total number of write attempts')
WRITE_ERRORS = Counter('pg_write_errors_total', 'Total number of failed writes')
FAILOVER_DURATION = Histogram(
    'pg_failover_duration_seconds', 
    'Time taken for writes to recover after a failure', 
    buckets=[0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0, 60.0, 120.0]
)

# Configuration from Environment Variables
DB_HOST = os.getenv('DB_HOST', 'postgres')
DB_PORT = os.getenv('DB_PORT', '5432')
DB_USER = os.getenv('DB_USER', 'postgres')
DB_PASS = os.getenv('DB_PASS', 'SuperSecretPassword123')
DB_NAME = os.getenv('DB_NAME', 'postgres')
POLL_INTERVAL = float(os.getenv('POLL_INTERVAL', '1.0'))

def setup_db():
    """Ensure the probe table exists before starting tests."""
    try:
        conn = psycopg2.connect(host=DB_HOST, port=DB_PORT, user=DB_USER, password=DB_PASS, dbname=DB_NAME, connect_timeout=5)
        conn.autocommit = True
        with conn.cursor() as cur:
            cur.execute("""
                CREATE TABLE IF NOT EXISTS ha_probe_metrics (
                    id SERIAL PRIMARY KEY,
                    ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )
            """)
        conn.close()
        logging.info("Database setup complete.")
        return True
    except Exception as e:
        logging.error(f"Failed to setup database: {e}")
        return False

def run_probe():
    # Start the Prometheus HTTP metrics endpoint
    start_http_server(8000)
    logging.info("Prometheus metrics server started on port 8000")
    
    # Wait until DB is ready
    while not setup_db():
        logging.info("Retrying database setup in 5 seconds...")
        time.sleep(5)
    
    outage_start = None
    
    while True:
        WRITE_ATTEMPTS.inc()
        try:
            # We explicitly open and close connection per attempt to simulate a true new connection 
            # (since failover kills existing connections anyway). A connection pool could also be used.
            conn = psycopg2.connect(
                host=DB_HOST, 
                port=DB_PORT, 
                user=DB_USER, 
                password=DB_PASS, 
                dbname=DB_NAME, 
                connect_timeout=3
            )
            conn.autocommit = True
            with conn.cursor() as cur:
                cur.execute("INSERT INTO ha_probe_metrics (ts) VALUES (now())")
            conn.close()
            
            # If we just recovered from an outage
            if outage_start is not None:
                duration = time.time() - outage_start
                FAILOVER_DURATION.observe(duration)
                logging.info(f"Recovered from outage. Failover duration: {duration:.2f} seconds")
                outage_start = None
            
        except Exception as e:
            WRITE_ERRORS.inc()
            if outage_start is None:
                outage_start = time.time()
                logging.warning(f"Write failed. Outage started: {e}")
            else:
                # Still in outage
                pass
        
        time.sleep(POLL_INTERVAL)

if __name__ == '__main__':
    run_probe()
