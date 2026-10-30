// signer polls the quartermaster's sign queue, issues licenses with the
// offline signing key, and posts results back. Runs on trusted hardware
// only — this is the one program that touches signing.key.
package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"quartermaster/license"
)

const quartermasterBaseURL = "http://10.46.0.1:9090"

func loadSigningKey(path string) (ed25519.PrivateKey, error) {
	hexBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	keyBytes, err := hex.DecodeString(string(hexBytes))
	if err != nil {
		return nil, err
	}
	return ed25519.PrivateKey(keyBytes), nil
}

type signRequest struct {
	ID      string `json:"id"`
	Product string `json:"product"`
	Email   string `json:"email"`
	Seats   int    `json:"seats"`
}

func pollOnce(baseURL string) (*signRequest, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	target := baseURL + "/queue/wait"
	if pollMinAge > 0 {
		target += fmt.Sprintf("?min_age=%d", pollMinAge)
	}
	resp, err := client.Get(target)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var req signRequest
	if err := json.NewDecoder(resp.Body).Decode(&req); err != nil {
		return nil, err
	}
	return &req, nil
}

func issueFor(priv ed25519.PrivateKey, req *signRequest) (string, error) {
	id, err := license.NewID()
	if err != nil {
		return "", err
	}
	l := license.License{
		Product:  req.Product,
		ID:       id,
		MajorVer: 1,
		Seats:    uint16(req.Seats),
		IssuedAt: time.Now().UTC(),
	}
	key, err := license.Issue(priv, l)
	if err != nil {
		return "", err
	}
	return license.Format(key), nil
}

func postComplete(baseURL, id, licenseKey string) error {
	body, _ := json.Marshal(map[string]string{
		"id":          id,
		"license_key": licenseKey,
	})
	resp, err := http.Post(baseURL+"/queue/complete", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return errAlreadySigned
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("complete failed: status %d", resp.StatusCode)
	}
	return nil
}

func postReject(baseURL, id, note string) error {
	body, _ := json.Marshal(map[string]string{
		"id":          id,
		"reject_note": note,
	})
	resp, err := http.Post(baseURL+"/queue/complete", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reject failed: status %d", resp.StatusCode)
	}
	return nil
}

// pollMinAge is SIGNER_MIN_AGE in seconds: 0 for the primary signer.
var pollMinAge int

var errAlreadySigned = fmt.Errorf("request was already signed")

func main() {
	priv, err := loadSigningKey("signing.key")
	if err != nil {
		log.Fatal("loading signing key: ", err)
	}
	log.Println("signing key loaded,", len(priv), "bytes")
	age, err := minAgeFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	pollMinAge = age
	if age > 0 {
		log.Printf("backup mode: claiming only requests older than %ds", age)
	}

	for {
		req, err := pollOnce(quartermasterBaseURL)
		if err != nil {
			log.Println("poll error:", err, "- retrying in 5s")
			time.Sleep(5 * time.Second)
			continue
		}
		if req == nil {
			continue // WaitPending already blocked; loop straight back
		}
		log.Printf("got request: id=%v product=%v seats=%v", req.ID, req.Product, req.Seats)

		key, err := issueFor(priv, req)
		if err != nil {
			log.Println("issue failed:", err)
			if err := postReject(quartermasterBaseURL, req.ID, err.Error()); err != nil {
				log.Println("reject post failed:", err)
			}
			continue
		}
		log.Println("issued key for request", req.ID)

		if err := postComplete(quartermasterBaseURL, req.ID, key); err != nil {
			if err == errAlreadySigned {
				log.Println("request", req.ID, "was already signed by another signer; discarding this key")
			} else {
				log.Println("complete post failed:", err)
			}
		} else {
			log.Println("posted complete")
		}
	}
}
