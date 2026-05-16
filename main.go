package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:11436"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tags", handleTags)
	mux.HandleFunc("POST /api/show", handleShow)
	mux.HandleFunc("POST /api/chat", handleChat)

	log.Printf("claudama listening on http://%s", addr)
	if err := http.ListenAndServe(addr, logRequests(mux)); err != nil {
		log.Fatal(err)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("CLAUDAMA_DEBUG") != "" && r.Body != nil && r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			log.Printf("%s %s body=%s", r.Method, r.URL.Path, string(body))
		} else {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}
