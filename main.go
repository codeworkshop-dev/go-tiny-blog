package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/boltdb/bolt"
	"github.com/gorilla/mux"
	"github.com/gosimple/slug"
)

// SiteMetaData is general information about the Site
type SiteMetaData struct {
	Title       string
	Description string
}

// Post is the data required to render the HTML template for the post page.
type Post struct {
	Author     string    `json:"author,omitempty"`
	Body       string    `json:"body,omitempty"`
	DatePosted time.Time `json:"datePosted,omitempty"`
	Title      string    `json:"title,omitempty"`
}

// PostMap is a map of posts with the slug as the key.
type PostMap map[string]Post

// Test Data

var siteMetaData = SiteMetaData{
	Title:       "Go Tiny Blog",
	Description: "A simple to reason about blog demonstrating the anatomy of a web app.",
}

// HomePageData is the data required to render the HTML template for the home page.
// It is made up of the site meta data, and a map of all of the posts.
type HomePageData struct {
	SiteMetaData SiteMetaData
	Posts        PostMap
}

// PostPageData is the data required to render the HTML template for the post page.
// It is made up of the site meta data, and a Post struct.
type PostPageData struct {
	SiteMetaData SiteMetaData
	Post         Post
}

func main() {

	db, err := setupDB()
	defer db.Close()

	if err != nil {
		log.Println(err)
	}

	r := newRouter(db)
	// Create http server and run inside go routine for graceful shutdown.
	srv := &http.Server{
		Handler:      r,
		Addr:         "127.0.0.1:8000",
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}
	log.Println("Starting up..")

	// This code is all about gracefully shutting down the web server.
	// This allows the server to resolve any pending requests before shutting down.
	// This works by running the web server in a go routine.
	// The main function then continues and blocks waiting for a kill signal from the os.
	// It intercepts the kill signal, shuts down the server by calling the Shutdown method.
	// Then exits when that is done.
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

// Load and parse the html templates to be used.
var homeTemplate = template.Must(template.ParseFiles("templates/home.html"))
var postTemplate = template.Must(template.ParseFiles("templates/post.html"))

// homeHandler returns the list of blog posts rendered in an HTML template.
func homeHandler(db *bolt.DB) http.HandlerFunc {
	fn := func(res http.ResponseWriter, r *http.Request) {
		postData, err := listPosts(db)
		if err != nil {
			res.Header().Set("Content-Type", "text/plain; charset=UTF-8")
			res.WriteHeader(http.StatusInternalServerError)
			res.Write([]byte("Could not list posts."))
			return
		}
		log.Println("Requested the home page.")
		res.Header().Set("Content-Type", "text/html; charset=UTF-8")
		res.WriteHeader(http.StatusOK)
		homeTemplate.Execute(res, HomePageData{SiteMetaData: siteMetaData, Posts: postData})
	}

	return fn
}

// postHandler looks up a specific blog post and returns it as an HTML template.
func getPostHandler(db *bolt.DB) http.HandlerFunc {

	fn := func(res http.ResponseWriter, r *http.Request) {
		// Get the URL param named slug from the response.
		slug := mux.Vars(r)["slug"]
		post, err := getPost(db, slug)
		if err != nil {
			res.Header().Set("Content-Type", "text/plain; charset=UTF-8")
			res.WriteHeader(http.StatusNotFound)
			res.Write([]byte("404 Page Not Found"))
			return
		}
		log.Println("Requested post.")
		res.Header().Set("Content-Type", "text/html; charset=UTF-8")
		res.WriteHeader(http.StatusOK)
		postTemplate.Execute(res, PostPageData{SiteMetaData: siteMetaData, Post: *post})
	}
	return fn
}

// createPostHandler handles posted JSON data representing a new post, and stores it in the database.
// It creates a slug to use as a key using the title of the post.
// This implies in the current state of affairs that titles must be unique or the keys will overwrite each other.
func createPostHandler(db *bolt.DB) http.HandlerFunc {
	fn := func(res http.ResponseWriter, r *http.Request) {
		var post Post
		res.Header().Set("Content-Type", "text/plain; charset=UTF-8")
		body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
		if err != nil {
			panic(err)
		}
		if err := r.Body.Close(); err != nil {
			panic(err)
		}
		if err := json.Unmarshal(body, &post); err != nil {
			res.Header().Set("Content-Type", "application/json; charset=UTF-8")
			res.WriteHeader(422) // unprocessable entity
			if err := json.NewEncoder(res).Encode(err); err != nil {
				panic(err)
			}
		}
		err = addPost(db, post, slug.Make(post.Title))
		if err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			res.Write([]byte("Error writing to DB."))
			return
		}
		res.Header().Set("Content-Type", "application/json; charset=UTF-8")
		res.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(res).Encode(post); err != nil {
			panic(err)
		}
	}
	return fn
}

// DATA STORE FUNCTIONS

// addPost writes a post to the boltDB KV store using the slug as a key, and a serialized post struct as the value.
func addPost(db *bolt.DB, post Post, slug string) error {

	// Marshal post struct into bytes which can be written to Bolt.
	buf, err := json.Marshal(post)
	if err != nil {
		return err
	}

	err = db.Update(func(tx *bolt.Tx) error {
		err := tx.Bucket([]byte("BLOG")).Bucket([]byte("POSTS")).Put([]byte(slug), []byte(buf))
		if err != nil {
			return fmt.Errorf("could not insert content: %v", err)
		}
		return nil
	})
	return err
}

// listPosts returns a map of posts indexed by the slug.
func listPosts(db *bolt.DB) (PostMap, error) {
	results := PostMap{}
	err := db.View(func(tx *bolt.Tx) error {
		// Assume bucket exists and has keys
		b := tx.Bucket([]byte("BLOG")).Bucket([]byte("POSTS"))

		c := b.Cursor()

		for k, v := c.First(); k != nil; k, v = c.Next() {
			post := Post{}
			if err := json.Unmarshal(v, &post); err != nil {
				panic(err)
			}
			results[string(k)] = post
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// getPost gets a specific post from the database by the slug.
func getPost(db *bolt.DB, slug string) (*Post, error) {
	result := Post{}
	err := db.View(func(tx *bolt.Tx) error {
		// Assume bucket exists and has keys
		b := tx.Bucket([]byte("BLOG")).Bucket([]byte("POSTS"))
		v := b.Get([]byte(slug))
		if err := json.Unmarshal(v, &result); err != nil {
			panic(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// INITIALIZATION FUNCTIONS
// setupDB sets up the database when the program start.
//  First it connects to the database, then it creates the buckets required to run the app if they do not exist.
func setupDB() (*bolt.DB, error) {
	db, err := bolt.Open("tinyblog.db", 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("could not open db, %v", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		root, err := tx.CreateBucketIfNotExists([]byte("BLOG"))
		if err != nil {
			return fmt.Errorf("could not create root bucket: %v", err)
		}
		_, err = root.CreateBucketIfNotExists([]byte("POSTS"))
		if err != nil {
			return fmt.Errorf("could not create post bucket: %v", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("could not set up buckets, %v", err)
	}
	fmt.Println("DB Setup Done")
	return db, nil
}

// newRouter configures and sets up the gorilla mux router paths and connects the route to the handler function.
func newRouter(db *bolt.DB) *mux.Router {
	r := mux.NewRouter()
	r.StrictSlash(true)
	r.HandleFunc("/", homeHandler(db)).Methods("GET")
	r.HandleFunc("/", createPostHandler(db)).Methods("POST")
	r.HandleFunc("/{slug}", getPostHandler(db)).Methods("GET")
	return r
}
