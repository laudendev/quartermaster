// quartermaster: webhook intake, sign queue, license delivery.
package main

import (
	"os"
	"log"
        "net/http"
	"quartermaster/store"
)

func main() {
	st, err := store.Open("quartermaster.db")
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	log.Println("store open")

	qa := &queueAPI{st: st}
	queueMux := http.NewServeMux()
	queueMux.HandleFunc("GET /queue/wait", qa.wait)
	queueMux.HandleFunc("POST /queue/complete", qa.complete)

	sa := &stripeAPI{st: st, secret: requireEnv("STRIPE_WEBHOOK_SECRET")}

	webhookMux := http.NewServeMux()
	webhookMux.HandleFunc("POST /stripe/webhook", sa.webhook)

	queueSrv := &http.Server{
		Addr:    "10.46.0.1:9090",
		Handler: queueMux,
	}
	webhookSrv := &http.Server{
		Addr:  "127.0.0.1:6773",
		Handler: webhookMux,
	}

	go func() {
	    log.Println("webhook server on", webhookSrv.Addr)
	    log.Fatal(webhookSrv.ListenAndServe())
        }()

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
