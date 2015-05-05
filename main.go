// A database for jokes.
package main

import (
	"crypto/sha512"
	"database/sql"
	"fmt"
	"github.com/twinj/uuid"
	htpl "html/template"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"
)

func init() {
	log.SetFlags(log.Lshortfile)
	f, err := os.OpenFile("log.txt", os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		log.Print(err)
	} else {
		log.SetOutput(f)
	}
}

var templates = htpl.Must(htpl.New("").Funcs(htpl.FuncMap{"AllCategories": AllCategories, "AllJokes": AllJokes, "ProposedJokes": ProposedJokes, "DefaultTitle": DefaultTitle}).ParseGlob("*.html"))

type Joke struct {
	JokeID     uint64
	Joke       string
	Reply      string
	Likes      uint64
	Date       time.Time
	CategoryID uint64

	Liked    bool
	Category *Category
}

type Category struct {
	CategoryID uint64
	Name       string
	Slug       string

	Jokes   []*Joke
	OrderBy string
}

/*

type TweetButton struct {
	Text    string
	Via     string
	Related string
	Url     string
	Count   string
}
*/

type NetError struct {
	Code    int
	Message string
}

func DefaultTitle() string {
	return IndexTitle
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func (j *Joke) AbsUrl() string {
	return Domain + PathJoke + strconv.FormatUint(j.JokeID, 10)
}
func (j *Joke) Title() string {
	return SiteTitle + " | " + JokeString + ": " + j.Joke[:min(15, len(j.Joke))] + "..."
}

/*
func (j *Joke) TweetButton() *TweetButton {
	t := &TweetButton{}
	t.Via = "barzedette"
	t.Related = "penpoe"
	if len(j.Joke) <= 140-len(" via @barzedette") {
		t.Text = j.Joke
		t.Count = "none"
		return t
	}

	suffix := "... via " + j.AbsUrl()
	t.Text = j.Joke[:min(len(j.Joke), 140-len(suffix))] + "..."
	t.Url = j.AbsUrl()
	return t
}
*/
func (j *Joke) WasLiked(r *http.Request) {
	c, err := r.Cookie("uuid")
	if err != nil {
		return
	}

	u, err := uuid.ParseUUID(c.Value)
	if err != nil {
		return
	}
	placeholder := 0
	err = DB.QueryRow(`select JokeID from Liked where uuid=? and JokeID=?;`, u.Bytes(), j.JokeID).Scan(&placeholder)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Print(err)
		}
		return
	}

	j.Liked = true
}

func (c *Category) AbsUrl() string {
	return Domain + PathCategory + c.Slug
}
func (c *Category) Title() string {
	return SiteTitle + c.Name
}

func AllCategories() ([]*Category, error) {
	rows, err := DB.Query(`select CategoryID, Name, Slug from Categories order by Name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var categories []*Category
	for rows.Next() {
		var c Category
		err := rows.Scan(&c.CategoryID, &c.Name, &c.Slug)
		if err != nil {
			return nil, err
		}
		categories = append(categories, &c)
	}
	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return categories, nil
}

func AllJokes() ([]*Joke, error) {
	rows, err := DB.Query(`select JokeID, Joke, Likes, Date, CategoryID from Jokes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jokes []*Joke
	for rows.Next() {
		var j Joke
		err := rows.Scan(&j.JokeID, &j.Joke, &j.Likes, &j.Date, &j.CategoryID)
		if err != nil {
			return nil, err
		}
		jokes = append(jokes, &j)
	}
	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return jokes, nil
}

func ProposedJokes() ([]*Joke, error) {
	rows, err := DB.Query(`select rowid, Joke from proposed_jokes;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jokes []*Joke
	for rows.Next() {
		var j Joke
		err := rows.Scan(&j.JokeID, &j.Joke)
		if err != nil {
			return nil, err
		}
		jokes = append(jokes, &j)
	}
	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return jokes, nil
}

func getCategoryByID(id uint64) (*Category, error) {
	c := &Category{}
	err := DB.QueryRow(`select Name, Slug from Categories where CategoryID=?`, id).Scan(&c.Name, &c.Slug)
	if err != nil {
		return nil, err
	}
	c.CategoryID = id
	return c, nil
}

type myHandler func(http.ResponseWriter, *http.Request) *NetError

func errorHandler(h myHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nerr := h(w, r)
		if nerr != nil {
			if nerr.Code == 404 {
				log.Printf("Path %q not found: %s", r.URL.Path, nerr.Message)
				w.WriteHeader(404)
				err := templates.ExecuteTemplate(w, "404.html", nil)
				if err != nil {
					http.NotFound(w, r)
				}
			} else {
				log.Printf("Path %q error: %s", r.URL.Path, nerr.Message)
				w.WriteHeader(500)
				err := templates.ExecuteTemplate(w, "500.html", nerr.Message)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			}
		}
	}
}

func jokeHandler(w http.ResponseWriter, r *http.Request) *NetError {
	bidstr := r.URL.Path[len(PathJoke):]
	if bidstr == "" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return nil
	}
	bid, err := strconv.ParseUint(bidstr, 10, 64)
	if err != nil {
		return &NetError{404, err.Error()}
	}

	b := &Joke{}
	err = DB.QueryRow(`select Joke, Reply, Likes, Date, CategoryID from Jokes where JokeID=?;`, bid).Scan(&b.Joke, &b.Reply, &b.Likes, &b.Date, &b.CategoryID)
	if err != nil {
		if err == sql.ErrNoRows {
			return &NetError{404, err.Error()}
		} else {
			return &NetError{500, err.Error()}
		}
	}
	b.JokeID = bid
	b.WasLiked(r)

	b.Category, err = getCategoryByID(b.CategoryID)
	if err != nil {
		return &NetError{500, err.Error()}
	}

	err = templates.ExecuteTemplate(w, "joke-page.html", b)
	if err != nil {
		return &NetError{500, err.Error()}
	}

	return nil
}

func categoryHandler(w http.ResponseWriter, r *http.Request) *NetError {
	slug := r.URL.Path[len(PathCategory):]
	if slug == "" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return nil
	}

	c := &Category{}
	err := DB.QueryRow(`select CategoryID, Name from Categories where Slug=?`, slug).Scan(&c.CategoryID, &c.Name)
	if err != nil {
		if err == sql.ErrNoRows {
			return &NetError{404, err.Error()}
		} else {
			return &NetError{500, err.Error()}
		}
	}
	c.Slug = slug

	o := ""
	switch r.URL.Query().Get("orderby") {
	case "newer":
		o = "Date desc;"
		c.OrderBy = "newer"
	case "older":
		o = "Date asc;"
		c.OrderBy = "older"
	default:
		o = "Likes desc;"
		c.OrderBy = "likes"
	}
	rows, err := DB.Query(`select JokeID,Joke,Reply,Likes from Jokes where CategoryID=? order by `+o, c.CategoryID)
	if err != nil {
		if err == sql.ErrNoRows {
			return &NetError{404, err.Error()}
		} else {
			return &NetError{500, err.Error()}
		}
	}
	defer rows.Close()
	var jokes []*Joke
	for rows.Next() {
		var j Joke
		err := rows.Scan(&j.JokeID, &j.Joke, &j.Reply, &j.Likes)
		if err != nil {
			return &NetError{500, err.Error()}
		}
		j.WasLiked(r)
		j.Category = c
		jokes = append(jokes, &j)
	}
	err = rows.Err()
	if err != nil {
		return &NetError{500, err.Error()}
	}

	c.Jokes = jokes
	err = templates.ExecuteTemplate(w, "category.html", c)
	if err != nil {
		return &NetError{500, err.Error()}
	}

	return nil
}

var countBeforePurge = 0

func likeHandler(w http.ResponseWriter, r *http.Request) *NetError {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return &NetError{500, err.Error()}
	}
	bid, err := strconv.ParseUint(string(body), 10, 64)
	if err != nil {
		return &NetError{500, err.Error()}
	}

	var u uuid.UUID
	c, err := r.Cookie("uuid")
	if err != nil {
		u = uuid.NewV4()
		http.SetCookie(w, &http.Cookie{Name: "uuid", Value: u.String(), Expires: time.Now().Add(60 * 24 * time.Hour)})
	} else {
		u, err = uuid.ParseUUID(c.Value)
		if err != nil {
			return &NetError{500, err.Error()}
		}

		placeholder := 0
		err = DB.QueryRow(`SELECT JokeID FROM Liked WHERE UUID=? and JokeID=?;`, u.Bytes(), bid).Scan(&placeholder)
		if err == nil {
			/* already liked. Actually shouldn't happen. */
			return nil
		}
		/* error */
		if err != sql.ErrNoRows {
			return &NetError{500, err.Error()}
		}
	}

	countBeforePurge++
	if countBeforePurge > 10000 {
		_, err = DB.Exec(`Delete from liked where date < ?;`, time.Now().Add(-60*24*time.Hour).Unix())
		if err != nil {
			log.Print(err)
		}
		countBeforePurge = 0
	}

	_, err = DB.Exec(`UPDATE Jokes SET likes=likes+1 WHERE JokeID=?;`, bid)
	if err != nil {
		return &NetError{500, err.Error()}
	}
	_, err = DB.Exec(`INSERT INTO Liked(Uuid, JokeID, date) VALUES(?,?,?);`, u.Bytes(), bid, time.Now().Unix())
	if err != nil {
		return &NetError{500, err.Error()}
	}

	return nil
}

func rootHandler(w http.ResponseWriter, r *http.Request) *NetError {
	if r.URL.Path == "" || r.URL.Path == "/" {
		http.Redirect(w, r, "/index.html", http.StatusMovedPermanently)
		return nil
	} else if r.URL.Path == "/index.html" {
		rows, err := DB.Query(`select JokeID,Joke,Reply,Likes,CategoryID from Jokes order by date desc limit 20;`)
		if err != nil {
			if err == sql.ErrNoRows {
				return &NetError{404, err.Error()}
			} else {
				return &NetError{500, err.Error()}
			}
		}
		defer rows.Close()
		var jokes []*Joke
		for rows.Next() {
			var j Joke
			err := rows.Scan(&j.JokeID, &j.Joke, &j.Reply, &j.Likes, &j.CategoryID)
			if err != nil {
				return &NetError{500, err.Error()}
			}
			j.WasLiked(r)
			j.Category, err = getCategoryByID(j.CategoryID)
			if err != nil {
				return &NetError{500, err.Error()}
			}
			jokes = append(jokes, &j)
		}
		err = rows.Err()
		if err != nil {
			return &NetError{500, err.Error()}
		}

		err = templates.ExecuteTemplate(w, "index.html", jokes)
		if err != nil {
			return &NetError{500, err.Error()}
		}
		return nil
	} else {
		p := r.URL.Path[len("/"):]

		if path.Ext(p) == ".html" {
			err := templates.ExecuteTemplate(w, p, nil)
			if err != nil {
				return &NetError{500, err.Error()}
			}
		} else {
			f, err := os.Open(p)
			if err != nil {
				return &NetError{404, err.Error()}
			}
			w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(p)))
			io.Copy(w, f)
			return nil
		}
	}

	return nil
}

func submitHandler(w http.ResponseWriter, r *http.Request) *NetError {
	if r.Method == "GET" {
		err := templates.ExecuteTemplate(w, "submit.html", nil)
		if err != nil {
			return &NetError{500, err.Error()}
		}
	} else if r.Method == "POST" {
		r.ParseForm()
		_, err := DB.Exec(`INSERT INTO proposed_jokes VALUES(?);`, r.PostForm.Get("joke-submit"))
		if err != nil {
			return &NetError{500, err.Error()}
		}
		err = templates.ExecuteTemplate(w, "submit-success.html", nil)
		if err != nil {
			return &NetError{500, err.Error()}
		}

	} else {
		return &NetError{500, "can't handle verb"}
	}

	return nil
}

func adminHandler(w http.ResponseWriter, r *http.Request) *NetError {
	var passwd string
	c, err := r.Cookie("password")
	if err != nil {
		if r.Method == "GET" {
			err = templates.ExecuteTemplate(w, "passwd.html", nil)
			if err != nil {
				return &NetError{500, err.Error()}
			}
			return nil
		} else if r.Method == "POST" {
			r.ParseForm()
			passwd = fmt.Sprintf("%x", sha512.Sum512([]byte(r.PostForm.Get("password"))))
			http.SetCookie(w, &http.Cookie{Name: "password", Value: passwd, Expires: time.Now().Add(60 * 24 * time.Hour)})
			http.Redirect(w, r, PathAdmin, http.StatusSeeOther)
			return nil
		} else {
			return &NetError{500, "can't handle verb"}
		}
	} else {
		passwd = c.Value
	}

	if passwd != Sha512passwd {
		http.SetCookie(w, &http.Cookie{Name: "password", MaxAge: -1})
		return nil
	}

	if r.Method == "GET" {
		err := templates.ExecuteTemplate(w, "admin.html", nil)
		if err != nil {
			return &NetError{500, err.Error()}
		}
	} else if r.Method == "POST" {
		r.ParseForm()
		var c uint64

		if new := r.PostForm.Get("new-category"); new != "" {
			res, err := DB.Exec(`INSERT INTO Categories(Name, Slug) Values(?,?);`, new, r.PostForm.Get("slug"))
			if err != nil {
				return &NetError{500, err.Error()}
			}

			c64, err := res.LastInsertId()
			if err != nil {
				return &NetError{500, err.Error()}
			}
			c = uint64(c64)

		} else {
			c, err = strconv.ParseUint(r.PostForm.Get("categoria"), 10, 64)
			if err != nil {
				return &NetError{500, err.Error()}
			}
		}

		_, err = DB.Exec(`INSERT INTO Jokes(Joke,Likes,Date,CategoryID) VALUES(?,?,?,?);`, r.PostForm.Get("joke-submit"), 0, time.Now(), c)
		if err != nil {
			return &NetError{500, err.Error()}
		}

		p, err := strconv.ParseUint(r.PostForm.Get("proposed"), 10, 64)
		if err != nil {
			return &NetError{500, err.Error()}
		}
		_, err = DB.Exec(`DELETE FROM proposed_jokes WHERE rowid=?;`, p)
		if err != nil {
			return &NetError{500, err.Error()}
		}
		http.Redirect(w, r, PathAdmin, http.StatusSeeOther)
	} else {
		return &NetError{500, "can't handle verb"}
	}

	return nil
}

func sitemapHandler(w http.ResponseWriter, r *http.Request) *NetError {
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(r.URL.Path)))
	err := templates.ExecuteTemplate(w, "sitemap.html", nil)
	if err != nil {
		return &NetError{500, err.Error()}
	}
	return nil
}

func main() {
	p := ":8080"
	if len(os.Args) > 1 {
		p = os.Args[1]
	}
	http.HandleFunc(PathJoke, errorHandler(jokeHandler))
	http.HandleFunc(PathCategory, errorHandler(categoryHandler))
	http.HandleFunc("/like", errorHandler(likeHandler))
	http.HandleFunc(PathSubmit, errorHandler(submitHandler))
	http.HandleFunc(PathAdmin, errorHandler(adminHandler))
	http.HandleFunc("/sitemap.txt", errorHandler(sitemapHandler))
	http.HandleFunc("/", errorHandler(rootHandler))
	err := http.ListenAndServe(p, nil)
	if err != nil {
		log.Fatal(err)
	}
}
