// quartermaster: webhook intake, sign queue, license delivery.
package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"quartermaster/activations"
	"quartermaster/queue"
)

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func loadPublicKey(path string) (ed25519.PublicKey, error) {
	hexBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	keyBytes, err := hex.DecodeString(string(hexBytes))
	if err != nil {
		return nil, err
	}
	return ed25519.PublicKey(keyBytes), nil
}

func main() {
	db, err := openDB("quartermaster.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	q, err := queue.Open(db)
	if err != nil {
		log.Fatal(err)
	}
	a, err := activations.Open(db)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("store open")

	qa := &queueAPI{st: q}
	queueMux := http.NewServeMux()
	queueMux.HandleFunc("GET /queue/wait", qa.wait)
	queueMux.HandleFunc("POST /queue/complete", qa.complete)

	sa := &stripeAPI{st: q, secret: requireEnv("STRIPE_WEBHOOK_SECRET")}

	pub, err := loadPublicKey("signing.pub")
	if err != nil {
		log.Fatal("loading public key: ", err)
	}

	aa := &activationAPI{st: a, pubs: []ed25519.PublicKey{pub}}

	webhookMux := http.NewServeMux()
	webhookMux.HandleFunc("POST /stripe/webhook", sa.webhook)
	webhookMux.HandleFunc("POST /license/activate", aa.activate)
	webhookMux.HandleFunc("POST /license/deactivate", aa.deactivate)

	queueSrv := &http.Server{
		Addr:    "10.46.0.1:9090",
		Handler: queueMux,
	}
	webhookSrv := &http.Server{
		Addr:    "127.0.0.1:6773",
		Handler: webhookMux,
	}

	go func() {
		log.Println("webhook server on", webhookSrv.Addr)
		log.Fatal(webhookSrv.ListenAndServe())
	}()

	go runEmailRetryLoop(q, 5*time.Minute)

	log.Println("queue API on", queueSrv.Addr)
	log.Fatal(queueSrv.ListenAndServe())
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var: %s", key)
	}
	return v
}
