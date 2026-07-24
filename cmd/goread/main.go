// Command goread runs the GoRead self-hosted RSS reader.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/Tiim/goread/internal/appdir"
	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/feed"
	"github.com/Tiim/goread/internal/server"
)

// shutdownTimeout bounds how long graceful shutdown waits for in-flight HTTP
// requests before giving up.
const shutdownTimeout = 10 * time.Second

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

	feeds := db.NewFeedRepo(sqlDB)
	articles := db.NewArticleRepo(sqlDB)
	refresher := feed.NewRefresher(feeds, articles)
	scheduler := feed.NewScheduler(feeds, refresher, feed.SchedulerInterval)

	ln, err := server.Listen(server.DefaultStartPort)
	if err != nil {
		return err
	}
	log.Printf("GoRead listening on http://%s", ln.Addr())

	handler := server.NewHandler(feeds, articles, scheduler)
	httpSrv := &http.Server{Handler: handler.Routes()}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scheduler.Run(ctx)
	}()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpSrv.Serve(ln)
	}()

	select {
	case err := <-serveErr:
		stop()
		wg.Wait()
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	case <-ctx.Done():
		log.Print("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("http server shutdown: %v", err)
		}
		// Waits for the scheduler's currently active refresh (if any) to
		// finish, per spec, before we return and close the DB.
		wg.Wait()
		return nil
	}
}
