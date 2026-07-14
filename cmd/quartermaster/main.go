// quartermaster: webhook intake, sign queue, license delivery.
package main

import (
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /queue/wait", qa.wait)
	mux.HandleFunc("POST /queue/complete", qa.complete)

	srv := &http.Server{
		Addr:    "127.0.0.1:9090", // dev; 10.8.0.1:9090 in prod config
		Handler: mux,
	}
	log.Println("queue API on", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
