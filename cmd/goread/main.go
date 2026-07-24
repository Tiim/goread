// Command goread runs the GoRead self-hosted RSS reader.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Tiim/goread/internal/appdir"
	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dbPath := os.Getenv("GOREAD_DB_PATH")
	if dbPath == "" {
		dataDir, err := appdir.DataDir()
		if err != nil {
			return err
		}
		dbPath = filepath.Join(dataDir, "goread.sqlite")
	}
	log.Printf("using database at %s", dbPath)

	sqlDB, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	ln, err := server.Listen(server.DefaultStartPort)
	if err != nil {
		return err
	}
	log.Printf("GoRead listening on http://%s", ln.Addr())

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("GoRead is starting up."))
	})

	return http.Serve(ln, mux)
}
