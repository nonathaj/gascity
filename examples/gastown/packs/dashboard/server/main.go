// Dashboard server for Gas City orchestration monitoring.
//
// This is a standalone HTTP server that provides an HTMX/SSE dashboard for
// visualizing Gas City state: convoys, agents, mail, rigs, sessions, events.
//
// Data sources:
//   - bd (beads CLI) for convoy, issue, mail, and hook data
//   - gc status/agent/events for city and agent status
//   - tmux for session previews
//   - gh for merge queue data
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port to listen on")
	cityPath := flag.String("city", "", "Path to the city directory")
	cityName := flag.String("city-name", "", "Name of the city")
	apiURL := flag.String("api", "", "GC API server URL (e.g. http://127.0.0.1:8080)")
	flag.Parse()

	if *cityPath == "" {
		log.Fatal("dashboard: -city flag is required")
	}
	if *cityName == "" {
		log.Fatal("dashboard: -city-name flag is required")
	}

	var fetcher ConvoyFetcher
	if *apiURL != "" {
		log.Printf("dashboard: using API server at %s", *apiURL)
		fetcher = NewAPIFetcher(*apiURL, *cityPath, *cityName)
	} else {
		fetcher = NewLiveConvoyFetcher(*cityPath, *cityName)
	}

	mux, err := NewDashboardMux(
		fetcher,
		*cityPath,
		*cityName,
		*apiURL,
		8*time.Second,  // fetchTimeout
		30*time.Second, // defaultRunTimeout
		60*time.Second, // maxRunTimeout
	)
	if err != nil {
		log.Fatalf("dashboard: failed to create handler: %v", err)
	}

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("dashboard: listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("dashboard: %v", err)
	}
}
