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
	"github.com/gomarkdown/markdown"
	"github.com/gorilla/mux"
	"github.com/gosimple/slug"
	"github.com/microcosm-cc/bluemonday"
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
	Slug       string    `json:"slug,omitempty"`
}

// PostMap is a map of posts with the slug as the key.
type PostMap map[string]Post

// Test Data

var siteMetaData = SiteMetaData{
	Title:       "Go Tiny Blog",
	Description: "A one file, simple to reason about blog designed to demonstrate the basic anatomy of a web app.",
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
	HTML         template.HTML
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

// homeHandler returns the list of blog posts rendered in an HTML template.
func homeHandler(db *bolt.DB, t *template.Template) http.HandlerFunc {
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
		t.Execute(res, HomePageData{SiteMetaData: siteMetaData, Posts: postData})
	}

	return fn
}

// createPostPageHandler serves the UI for creating a post. It is a form that submits to the create post REST endpoint.
func createPostPageHandler(db *bolt.DB, t *template.Template) http.HandlerFunc {
	fn := func(res http.ResponseWriter, r *http.Request) {
		log.Println("Requested the create post page.")
		res.Header().Set("Content-Type", "text/html; charset=UTF-8")
		res.WriteHeader(http.StatusOK)
		t.Execute(res, HomePageData{SiteMetaData: siteMetaData})
	}

	return fn
}

// postHandler looks up a specific blog post and returns it as an HTML template.
func getPostHandler(db *bolt.DB, t *template.Template) http.HandlerFunc {

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
		log.Printf("Requested: %s by %s \n", post.Title, post.Author)
		unsafePostHTML := markdown.ToHTML([]byte(post.Body), nil, nil)
		postHTML := bluemonday.UGCPolicy().SanitizeBytes(unsafePostHTML)
		res.Header().Set("Content-Type", "text/html; charset=UTF-8")
		res.WriteHeader(http.StatusOK)
		t.Execute(res, PostPageData{SiteMetaData: siteMetaData, Post: *post, HTML: template.HTML(postHTML)})
	}
	return fn
}

// editPostPageHandler serves the UI for creating a post. It is a form that submits to the create post REST endpoint.
func editPostPageHandler(db *bolt.DB, t *template.Template) http.HandlerFunc {
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
		log.Printf("Requested edit page for: %s by %s \n", post.Title, post.Author)
		res.Header().Set("Content-Type", "text/html; charset=UTF-8")
		res.WriteHeader(http.StatusOK)
		t.Execute(res, PostPageData{SiteMetaData: siteMetaData, Post: *post})
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
		// Reads in the body content from the post request safely limiting to max size.
		body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
		if err != nil {
			panic(err)
		}
		// Close the Reader.
		if err := r.Body.Close(); err != nil {
			panic(err)
		}

		// Convert the JSON to a Post struct and write it to the post variable created at the top
		// of the handler.
		if err := json.Unmarshal(body, &post); err != nil {
			res.Header().Set("Content-Type", "application/json; charset=UTF-8")
			res.WriteHeader(422) // unprocessable entity
			if err := json.NewEncoder(res).Encode(err); err != nil {
				panic(err)
			}
		}

		// Set the creation time stamp to the current server time.
		post.DatePosted = time.Now()

		// Create a URL safe slug from the timestamp and the title.
		autoSlug := fmt.Sprintf("%s-%s", slug.Make(post.DatePosted.Format(time.RFC3339)), slug.Make(post.Title))
		post.Slug = autoSlug

		if err = upsertPost(db, post, autoSlug); err != nil {
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

// modifyPostHandler is responsible for modifing the contents of a specific post.
// It accepts a new post object as JSON content in the request body.
// It writes the new post object to the URL slug value unlike the createPostHandler
// which generates a new slug using the post date and time. Notice this means you can not change the URI.
// This is left as homework for the reader.
func modifyPostHandler(db *bolt.DB) http.HandlerFunc {
	fn := func(res http.ResponseWriter, r *http.Request) {
		var post Post
		slug := mux.Vars(r)["slug"]
		res.Header().Set("Content-Type", "application/json; charset=UTF-8")
		body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
		if err != nil {
			panic(err)
		}
		if err := r.Body.Close(); err != nil {
			panic(err)
		}
		if err := json.Unmarshal(body, &post); err != nil {
			res.WriteHeader(422) // unprocessable entity
			if err := json.NewEncoder(res).Encode(err); err != nil {
				panic(err)
			}
		}
		post.Slug = slug
		post.DatePosted = time.Now()
		// Call the upsertPost function passing in the database, a post struct, and the slug.
		// If there is an error writing to the database write an error to the response and return.
		if err = upsertPost(db, post, slug); err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			res.Write([]byte("Error writing to DB."))
			return
		}
		res.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(res).Encode(post); err != nil {
			panic(err)
		}
	}
	return fn
}

// DeletePostHandler deletes the post with the key matching the slug in the URL.
func deletePostHandler(db *bolt.DB) http.HandlerFunc {
	fn := func(res http.ResponseWriter, r *http.Request) {
		res.Header().Set("Content-Type", "application/json; charset=UTF-8")
		slug := mux.Vars(r)["slug"]
		if err := deletePost(db, slug); err != nil {
			panic(err)
		}
		res.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(res).Encode(struct {
			Deleted bool
		}{
			true,
		}); err != nil {
			panic(err)
		}
	}
	return fn
}

// DATA STORE FUNCTIONS

// upsertPost writes a post to the boltDB KV store using the slug as a key, and a serialized post struct as the value.
// If the slug already exists the existing post will be overwritten.
func upsertPost(db *bolt.DB, post Post, slug string) error {

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
// TODO: We could we add pagination to this!
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
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// deletePost deletes a specific post by slug.
func deletePost(db *bolt.DB, slug string) error {
	err := db.Update(func(tx *bolt.Tx) error {
		err := tx.Bucket([]byte("BLOG")).Bucket([]byte("POSTS")).Delete([]byte(slug))
		if err != nil {
			return fmt.Errorf("could not delete content: %v", err)
		}
		return nil
	})
	return err
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

	// Load and parse the html templates to be used.
	homeTemplate := template.Must(template.ParseFiles("templates/home.html"))
	postTemplate := template.Must(template.ParseFiles("templates/post.html"))
	editTemplate := template.Must(template.ParseFiles("templates/edit-post.html"))
	createTemplate := template.Must(template.ParseFiles("templates/create-post.html"))
	r := mux.NewRouter()
	r.StrictSlash(true)
	r.HandleFunc("/", homeHandler(db, homeTemplate)).Methods("GET")
	r.HandleFunc("/", createPostHandler(db)).Methods("POST")
	r.HandleFunc("/create", createPostPageHandler(db, createTemplate)).Methods("GET")
	r.HandleFunc("/{slug}", getPostHandler(db, postTemplate)).Methods("GET")
	r.HandleFunc("/{slug}", modifyPostHandler(db)).Methods("POST")
	r.HandleFunc("/{slug}", deletePostHandler(db)).Methods("DELETE")

	r.HandleFunc("/{slug}/edit", editPostPageHandler(db, editTemplate)).Methods("GET")
	return r
}
