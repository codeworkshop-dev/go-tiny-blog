package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/mux"
)

// SiteMetaData is general information about the Site
type SiteMetaData struct {
	Title       string
	Description string
}

// Post is the data required to render the HTML template for the post page.
type Post struct {
	Author     string
	Body       string
	DatePosted time.Time
	Slug       string
	Title      string
}

// PostMap is a map of posts with the slug as the key.
type PostMap map[string]Post

// Test Data

var siteMetaData = SiteMetaData{
	Title:       "Go Tiny Blog",
	Description: "A simple to reason about blog demonstrating the anatomy of a web app.",
}

var posts = PostMap{
	"hello-world": {
		Author:     "Stephen",
		Body:       "The world welcomes you.",
		Title:      "Hello World",
		Slug:       "hello-world",
		DatePosted: time.Now(),
	}, "hello-mars": {
		Author:     "Stephen",
		Body:       "Mars welcomes you.",
		Title:      "Hello Mars",
		DatePosted: time.Now(),
	}, "hello-venus": {
		Author:     "Stephen",
		Body:       "Venus welcomes you.",
		Title:      "Hello Venus",
		DatePosted: time.Now(),
	},
}

// HomePageData is the data required to render the HTML template for the home page.
type HomePageData struct {
	SiteMetaData SiteMetaData
	Posts        PostMap
}

// PostPageData is the data required to render the HTML template for the post page.
type PostPageData struct {
	SiteMetaData SiteMetaData
	Post         Post
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

var homeTemplate = template.Must(template.ParseFiles("templates/home.html"))
var postTemplate = template.Must(template.ParseFiles("templates/post.html"))

// homeHandler returns the list of blog posts rendered in an HTML template.
func homeHandler(res http.ResponseWriter, r *http.Request) {
	log.Println("Requested the home page.")
	res.Header().Set("Content-Type", "text/html; charset=UTF-8")
	res.WriteHeader(http.StatusOK)
	homeTemplate.Execute(res, HomePageData{SiteMetaData: siteMetaData, Posts: posts})
}

// postHandler looks up a specific blog post and returns it as HTML.
func postHandler(res http.ResponseWriter, r *http.Request) {
	// Get the URL param named slug from the response.
	slug := mux.Vars(r)["slug"]
	post, ok := posts[slug]
	if !ok {
		res.Header().Set("Content-Type", "text/plain; charset=UTF-8")
		res.WriteHeader(http.StatusNotFound)
		res.Write([]byte("404 Page Not Found"))
		return
	}
	log.Println("Requested post.")
	res.Header().Set("Content-Type", "text/html; charset=UTF-8")
	res.WriteHeader(http.StatusOK)
	postTemplate.Execute(res, PostPageData{SiteMetaData: siteMetaData, Post: post})
}

// newRouter configures and sets up the gorilla mux router paths.
func newRouter() *mux.Router {
	r := mux.NewRouter()
	r.StrictSlash(true)
	r.HandleFunc("/", homeHandler).Methods("GET")
	r.HandleFunc("/{slug}", postHandler).Methods("GET")
	return r
}
