// Command server runs the tempora visualization and JSON API.
package main

import (
	"embed"
	"flag"
	"log"
	"net/http"

	"tempora/cmd/server/internal/srv"
)

//go:embed index.html
var staticFS embed.FS

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()
	page, err := staticFS.ReadFile("index.html")
	if err != nil {
		log.Fatal(err)
	}
	handler := srv.New(page).Handler()
	log.Printf("tempora listening on http://localhost%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
