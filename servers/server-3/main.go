package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
)

var (
	snippets   = make(map[int]Snippet)
	snippetsMu sync.RWMutex
	nextID     = 1
)

type Snippet struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Language string `json:"language"`
	Body     string `json:"body"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)

	mux.HandleFunc("POST /snippets", createSnippet)
	mux.HandleFunc("GET /snippets/{id}", getSnippet)
	mux.HandleFunc("DELETE /snippets/{id}", deleteSnippet)

	fmt.Println("Server is running on http://localhost:8083")
	http.ListenAndServe(":8083", mux)
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	snippetsMu.RLock()
	count := len(snippets)
	snippetsMu.RUnlock()
	fmt.Fprintf(w, "snippet store, %d snippet(s) on hand\n", count)
}

func createSnippet(w http.ResponseWriter, r *http.Request) {
	var s Snippet
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if s.Title == "" || s.Body == "" {
		http.Error(w, "title and body are required", http.StatusBadRequest)
		return
	}

	snippetsMu.Lock()
	s.ID = nextID
	nextID++
	snippets[s.ID] = s
	snippetsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(s)
}

func getSnippet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	snippetsMu.RLock()
	s, ok := snippets[id]
	snippetsMu.RUnlock()
	if !ok {
		http.Error(w, "snippet not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func deleteSnippet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	snippetsMu.Lock()
	_, ok := snippets[id]
	if ok {
		delete(snippets, id)
	}
	snippetsMu.Unlock()

	if !ok {
		http.Error(w, "snippet not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
