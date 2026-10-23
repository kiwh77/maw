// Command harness serves the production board page for real-browser acceptance.
// It is test-only: API data and SSE events are injected at this boundary while
// the HTML, CSS and application script come from internal/board as shipped.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
)

func fixtureTasks() []*db.Task {
	return []*db.Task{
		{ProjectID: "p-harness", ProjectName: "Harness", ID: "VN-981", Title: "Harness ready task", Status: "ready", Priority: 10, Owner: "trae-goalagents-1"},
		{ProjectID: "p-harness", ProjectName: "Harness", ID: "VN-982", Title: "Harness done task", Status: "done", Priority: 20, Owner: "trae-goalagents-1"},
		{ProjectID: "p-harness", ProjectName: "Harness", ID: "VN-983", Title: "Harness blocked task", Status: "blocked", Priority: 30, BlockedReason: "waiting for review"},
	}
}

func fixture() board.Snapshot {
	tasks := fixtureTasks()
	snap := board.BuildSnapshot(tasks, []*db.Agent{{
		ProjectID:   "p-harness",
		ProjectName: "Harness",
		ID:          "trae-goalagents-1",
		Role:        "worker",
		Status:      "working",
		TaskID:      "VN-981",
		LastSeen:    "2026-10-23T04:00:00Z",
	}}, 2)
	snap.Projects = []*db.Project{{
		ID:          "p-harness",
		Name:        "Harness",
		Path:        "/tmp/harness",
		MainBranch:  "main",
		MaxParallel: 2,
	}}
	snap.UpdatedAt = "2026-10-23 12:00:00"
	snap.NextHint = "Fixture injected at the test boundary"
	return snap
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:47121", "listen address")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, board.PageHTML())
	})
	mux.HandleFunc("/api/board", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, fixture())
	})
	mux.HandleFunc("/snapshot.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, fixture())
	})
	mux.HandleFunc("/api/task", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		for _, task := range fixtureTasks() {
			if task.ID == id {
				writeJSON(w, board.TaskDetail{
					Task:    task,
					Project: &db.Project{ID: "p-harness", Name: "Harness"},
					Events:  []*db.Event{},
					OK:      true,
				})
				return
			}
		}
		http.Error(w, "task not found", http.StatusNotFound)
	})
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		flusher.Flush()

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	})

	server := &http.Server{Addr: *addr, Handler: mux}
	_ = server.ListenAndServe()
}
