// A database for jokes.
package main

import (
	"crypto/sha512"
	"database/sql"
	"fmt"
	"github.com/twinj/uuid"
	htpl "html/template"
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

var templates = htpl.Must(htpl.New("").Funcs(htpl.FuncMap{"AllCategories": AllCategories, "GetJokes": GetJokes, "ProposedJokes": ProposedJokes, "DefaultTitle": DefaultTitle}).ParseGlob("*.html"))

type Joke struct {
	JokeID     uint64
	Joke       string
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
	return JokeString + ": " + string([]rune(j.Joke)[:min(15, len(j.Joke))]) + "..." + " | " + SiteTitle
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

func (cj *Joke) Next() *Joke {
	j := &Joke{}
	err := DB.QueryRow(`select JokeID, Joke, Likes, Date, CategoryID from Jokes
where JokeID > ? and CategoryID = ?
order by JokeID asc limit 1;`, cj.JokeID, cj.Category.CategoryID).Scan(&j.JokeID, &j.Joke, &j.Likes, &j.Date, &j.CategoryID)
	if err != nil {
		return nil
	}
	j.Category = cj.Category
	return j
}

func (cj *Joke) Prev() *Joke {
	j := &Joke{}
	err := DB.QueryRow(`select JokeID, Joke, Likes, Date, CategoryID from Jokes
where JokeID < ? and CategoryID = ?
order by JokeID desc limit 1;`, cj.JokeID, cj.Category.CategoryID).Scan(&j.JokeID, &j.Joke, &j.Likes, &j.Date, &j.CategoryID)
	if err != nil {
		return nil
	}
	j.Category = cj.Category
	return j
}

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
	return c.Name + " - " + SiteTitle
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

func orderBy(by string) string {
	switch by {
	case "newer":
		return " order by Date desc"
	case "older":
		return " order by Date asc"
	default:
		return " order by Likes desc"
	}
}

func GetJokes(categoryID uint64, order string, limit uint) ([]*Joke, error) {
	var rows *sql.Rows
	var err error

	var c *Category
	if categoryID > 0 {
		c, err = getCategoryByID(categoryID)
		if err != nil {
			return nil, err
		}
	}

	l := ""
	if limit > 0 {
		l = " limit " + fmt.Sprint(limit)
	}

	if categoryID == 0 {
		rows, err = DB.Query(`select JokeID, Joke, Likes, Date, CategoryID from Jokes ` + orderBy(order) + l + `;`)
	} else {
		rows, err = DB.Query(`select JokeID, Joke, Likes, Date, CategoryID from Jokes where CategoryID=? `+orderBy(order)+l+`;`, categoryID)
	}
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
		if categoryID > 0 {
			j.Category = c
		} else {
			j.Category, err = getCategoryByID(j.CategoryID)
			if err != nil {
				return nil, err
			}
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
	err := DB.QueryRow(`select Name, Slug from Categories where CategoryID=?;`, id).Scan(&c.Name, &c.Slug)
	if err != nil {
		return nil, err
	}
	c.CategoryID = id
	return c, nil
}

func getCategoryBySlug(slug string) (*Category, error) {
	c := &Category{}
	err := DB.QueryRow(`select CategoryID, Name from Categories where Slug=?;`, slug).Scan(&c.CategoryID, &c.Name)
	if err != nil {
		return nil, err
	}
	c.Slug = slug
	return c, nil
}

func getJokeByID(id uint64) (*Joke, error) {
	j := &Joke{}
	err := DB.QueryRow(`select Joke, Likes, Date, CategoryID from Jokes where JokeID=?;`, id).Scan(&j.Joke, &j.Likes, &j.Date, &j.CategoryID)
	if err != nil {
		return nil, err
	}
	j.JokeID = id
	j.Category, err = getCategoryByID(j.CategoryID)
	return j, err
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
	idstr := r.URL.Path[len(PathJoke):]
	if idstr == "" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return nil
	}
	id, err := strconv.ParseUint(idstr, 10, 64)
	if err != nil {
		return &NetError{404, err.Error()}
	}
	j, err := getJokeByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			return &NetError{404, err.Error()}
		} else {
			return &NetError{500, err.Error()}
		}
	}
	j.WasLiked(r)
	err = templates.ExecuteTemplate(w, "joke-page.html", j)
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
	c, err := getCategoryBySlug(slug)
	if err != nil {
		if err == sql.ErrNoRows {
			return &NetError{404, err.Error()}
		} else {
			return &NetError{500, err.Error()}
		}
	}
	switch r.URL.Query().Get("orderby") {
	case "newer":
		c.OrderBy = "newer"
	case "older":
		c.OrderBy = "older"
	default:
		c.OrderBy = "likes"
	}
	c.Jokes, err = GetJokes(c.CategoryID, c.OrderBy, 0)
	if err != nil {
		return &NetError{500, err.Error()}
	}
	for _, j := range c.Jokes {
		j.WasLiked(r)
	}
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

func staticHandler(w http.ResponseWriter, r *http.Request) *NetError {
	w.Header().Add("Cache-Control", "max-age=604800, public")
	http.ServeFile(w, r, r.URL.Path)
	return nil
}

func rootHandler(w http.ResponseWriter, r *http.Request) *NetError {
	p := r.URL.Path
	switch {
	case p == "" || p == "/index.html":
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return nil
	case p == "/":
		jokes, err := GetJokes(0, "newer", 20)
		if err != nil {
			if err == sql.ErrNoRows {
				return &NetError{404, err.Error()}
			} else {
				return &NetError{500, err.Error()}
			}
		}
		for _, j := range jokes {
			j.WasLiked(r)
		}
		err = templates.ExecuteTemplate(w, "index.html", jokes)
		if err != nil {
			return &NetError{500, err.Error()}
		}
		return nil
	case path.Ext(p) == ".html":
		w.Header().Add("Cache-Control", "max-age=86400, public")
		err := templates.ExecuteTemplate(w, p[1:], nil)
		if err != nil {
			return &NetError{500, err.Error()}
		}
	}
	return &NetError{404, ""}
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
			c, err = strconv.ParseUint(r.PostForm.Get("categoryid"), 10, 64)
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
	http.HandleFunc("/static/", errorHandler(staticHandler))
	http.HandleFunc("/", errorHandler(rootHandler))
	err := http.ListenAndServe(p, nil)
	if err != nil {
		log.Fatal(err)
	}
}
