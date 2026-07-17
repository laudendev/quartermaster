// quartermaster: webhook intake, sign queue, license delivery.
package main

import (
	"os"
	"log"
	"crypto/ed25519"
	"encoding/hex"
        "net/http"
	"quartermaster/store"
)

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

	pub, err := loadPublicKey("signing.pub")
	if err != nil {
	    log.Fatal("loading public key: ", err)
        }

	aa := &activationAPI{st: st, pub: pub}

	webhookMux := http.NewServeMux()
	webhookMux.HandleFunc("POST /stripe/webhook", sa.webhook)
	webhookMux.HandleFunc("POST /license/activate", aa.activate)
	webhookMux.HandleFunc("POST /license/deactivate", aa.deactivate)

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
