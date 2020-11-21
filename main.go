package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/mux"
)

func homeHandler(res http.ResponseWriter, r *http.Request) {
	res.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	res.WriteHeader(http.StatusOK)
	data := []byte("Home Page!")
	res.Write(data)
}

func main() {
	r := newRouter()
	// Create http server and run inside go routine for graceful shutdown.
	srv := &http.Server{
		Handler:      r,
		Addr:         "127.0.0.1:8000",
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}
	log.Println("Starting up..")
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()

	srv.Shutdown(ctx)
	log.Println("Shutting down..")
	os.Exit(0)
}

func newRouter() *mux.Router {
	r := mux.NewRouter()
	r.HandleFunc("/", homeHandler)
	return r
}
