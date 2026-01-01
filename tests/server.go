package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" {
		http.NotFound(w, r)
		return
	}
	cookie := http.Cookie{
		Name:     "test-cookie",
		Value:    "present",
		Expires:  time.Now().Add(24 * time.Hour),
		HttpOnly: false,
		Path:     "/",
	}
	http.SetCookie(w, &cookie)
	_, _ = fmt.Fprintf(w, "Cookie set!")
	log.Printf("Visited %s, set cookie: %s, User-Agent: %s", r.URL.Path, cookie.String(), r.UserAgent())
}

func main() {
	http.HandleFunc("/", rootHandler)
	port := "8080"
	log.Printf("Server starting on port %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
