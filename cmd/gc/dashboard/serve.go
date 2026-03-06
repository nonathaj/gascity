package dashboard

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// Serve starts the dashboard HTTP server. It creates an APIFetcher, builds
// the dashboard mux, and listens on the given port. This is the entry point
// called by the "gc dashboard serve" cobra command.
func Serve(port int, cityPath, cityName, apiURL string) error {
	log.Printf("dashboard: using API server at %s", apiURL)
	fetcher := NewAPIFetcher(apiURL, cityPath, cityName)

	mux, err := NewDashboardMux(
		fetcher,
		cityPath,
		cityName,
		apiURL,
		8*time.Second,  // fetchTimeout
		30*time.Second, // defaultRunTimeout
		60*time.Second, // maxRunTimeout
	)
	if err != nil {
		return fmt.Errorf("dashboard: failed to create handler: %w", err)
	}

	addr := fmt.Sprintf(":%d", port)
	log.Printf("dashboard: listening on http://localhost%s", addr)
	return http.ListenAndServe(addr, mux)
}
