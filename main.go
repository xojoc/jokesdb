// A database for jokes.
package main

import (
	"crypto/sha512"
	"database/sql"
	"fmt"
	"github.com/xojoc/web"
	"gopkg.in/gorp.v1"
	htpl "html/template"
	"log"
	"math/rand"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"
)

var (
	DB     *gorp.DbMap
	DBName string = "./jokes.db"
	// use package-local rand source to avoid excessive locking
	rng *rand.Rand
)

type Joke struct {
	JokeID     uint64
	Joke       string
	Date       time.Time
	CategoryID uint64

	Category *Category `db:"-"`
}

type Category struct {
	CategoryID uint64 `sql:"primary key not null"`
	Name       string `sql:"not null"`
	Slug       string `sql:"unique not null"`

	Jokes   []*Joke `db:"-"`
	OrderBy string  `db:"-"`
}

type ProposedJoke struct {
	JokeID uint64
	Joke   string
}

func init() {
	web.CreateLog("log.txt")
	db, err := sql.Open("sqlite3", DBName)
	if err != nil {
		log.Fatal(err)
	}
	DB = &gorp.DbMap{Db: db, Dialect: gorp.SqliteDialect{}}
	DB.AddTableWithName(Category{}, "categories").SetKeys(true, "CategoryID")
	DB.AddTableWithName(Joke{}, "jokes").SetKeys(true, "JokeID")
	DB.AddTableWithName(ProposedJoke{}, "proposed_jokes").SetKeys(true, "JokeID")
	DB.CreateTablesIfNotExists()
	web.Pages = htpl.Must(htpl.New("").Funcs(htpl.FuncMap{
		"AllCategories": AllCategories,
		"GetJokes":      GetJokes,
		"ProposedJokes": ProposedJokes,
		"DefaultTitle":  DefaultTitle}).ParseGlob("pages/*.html"))
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

func (cj *Joke) Next() *Joke {
	j := &Joke{}
	err := DB.SelectOne(j, `select * from Jokes where JokeID > ? and CategoryID = ?
						    order by JokeID asc limit 1;`, cj.JokeID, cj.Category.CategoryID)
	if err != nil {
		return nil
	}
	j.Category = cj.Category
	return j
}

func (cj *Joke) Prev() *Joke {
	j := &Joke{}
	err := DB.SelectOne(j, `select * from Jokes where JokeID < ? and CategoryID = ?
							order by JokeID desc limit 1;`, cj.JokeID, cj.Category.CategoryID)
	if err != nil {
		return nil
	}
	j.Category = cj.Category
	return j
}

func (c *Category) AbsUrl() string {
	return Domain + PathCategory + c.Slug
}
func (c *Category) Title() string {
	return JokeString + c.Name + " - " + SiteTitle
}

func AllCategories() ([]*Category, error) {
	var categories []*Category
	_, err := DB.Select(&categories, `select * from Categories order by name`)
	if err != nil {
		return nil, err
	}
	return categories, nil
}

func orderBy(by string) string {
	switch by {
	case "older":
		return " order by Date asc"
	default:
		return " order by Date desc"
	}
}

func GetJokes(categoryID uint64, order string, limit uint) ([]*Joke, error) {
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
	var jokes []*Joke
	if categoryID == 0 {
		_, err = DB.Select(&jokes, `select * from Jokes `+orderBy(order)+l+`;`)
	} else {
		_, err = DB.Select(&jokes, `select * from Jokes where CategoryID=? `+orderBy(order)+l+`;`, categoryID)
	}
	if err != nil {
		return nil, err
	}
	for _, j := range jokes {
		if categoryID > 0 {
			j.Category = c
		} else {
			j.Category, err = getCategoryByID(j.CategoryID)
			if err != nil {
				return nil, err
			}
		}
	}
	return jokes, nil
}

func ProposedJokes() ([]*Joke, error) {
	var jokes []*Joke
	_, err := DB.Select(&jokes, `select * from proposed_jokes;`)
	return jokes, err
}

func getCategoryByID(id uint64) (*Category, error) {
	obj, err := DB.Get(Category{}, id)
	return obj.(*Category), err
}
func getCategoryBySlug(slug string) (*Category, error) {
	c := &Category{}
	err := DB.SelectOne(c, `select * from Categories where Slug=?;`, slug)
	return c, err
}

func getJokeByID(id uint64) (*Joke, error) {
	obj, err := DB.Get(Joke{}, id)
	if err != nil {
		return nil, err
	}
	j := obj.(*Joke)
	j.Category, err = getCategoryByID(j.CategoryID)
	return j, err
}

func jokeHandler(w http.ResponseWriter, r *http.Request) *web.NetError {
	idstr := r.URL.Path[len(PathJoke):]
	if idstr == "" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return nil
	}
	id, err := strconv.ParseUint(idstr, 10, 64)
	if err != nil {
		return &web.NetError{404, err.Error()}
	}
	j, err := getJokeByID(id)
	if err != nil {
		return &web.NetError{404, err.Error()}
	}
	return web.ExecuteTemplate(w, "joke-page.html", j)
}

func categoryHandler(w http.ResponseWriter, r *http.Request) *web.NetError {
	slug := r.URL.Path[len(PathCategory):]
	if slug == "" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return nil
	}
	c, err := getCategoryBySlug(slug)
	if err != nil {
		return &web.NetError{404, err.Error()}
	}
	switch r.URL.Query().Get("orderby") {
	case "older":
		c.OrderBy = "older"
	default:
		c.OrderBy = "newer"
	}
	c.Jokes, err = GetJokes(c.CategoryID, c.OrderBy, 0)
	if err != nil {
		return &web.NetError{500, err.Error()}
	}
	return web.ExecuteTemplate(w, "category.html", c)
}

func staticHandler(w http.ResponseWriter, r *http.Request) *web.NetError {
	fmt.Print(r.URL.Path)
	w.Header().Add("Cache-Control", "max-age=604800, public")
	http.ServeFile(w, r, "."+r.URL.Path)
	return nil
}

func rootHandler(w http.ResponseWriter, r *http.Request) *web.NetError {
	p := r.URL.Path
	switch {
	case p == "" || p == "/index.html":
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return nil
	case p == "/":
		jokes, err := GetJokes(0, "newer", 20)
		if err != nil {
			return &web.NetError{500, err.Error()}
		}
		return web.ExecuteTemplate(w, "index.html", jokes)
	case path.Ext(p) == ".html":
		w.Header().Add("Cache-Control", "max-age=86400, public")
		return web.ExecuteTemplate(w, p[1:], nil)
	}
	return &web.NetError{404, ""}
}

func submitHandler(w http.ResponseWriter, r *http.Request) *web.NetError {
	if r.Method == "GET" {
		return web.ExecuteTemplate(w, "submit.html", nil)
	} else if r.Method == "POST" {
		r.ParseForm()
		j := &ProposedJoke{Joke: r.PostForm.Get("joke-submit")}
		err := DB.Insert(j)
		if err != nil {
			return &web.NetError{500, err.Error()}
		}
		return web.ExecuteTemplate(w, "submit-success.html", nil)
	} else {
		return &web.NetError{501, "can't handle verb"}
	}
}

func adminHandler(w http.ResponseWriter, r *http.Request) *web.NetError {
	var passwd string
	c, err := r.Cookie("password")
	if err != nil {
		if r.Method == "GET" {
			return web.ExecuteTemplate(w, "password.html", nil)
		} else if r.Method == "POST" {
			r.ParseForm()
			passwd = fmt.Sprintf("%x", sha512.Sum512([]byte(r.PostForm.Get("password"))))
			http.SetCookie(w, &http.Cookie{Name: "password", Value: passwd, Expires: time.Now().Add(60 * 24 * time.Hour)})
			http.Redirect(w, r, PathAdmin, http.StatusSeeOther)
			return nil
		} else {
			return &web.NetError{501, "can't handle verb"}
		}
	} else {
		passwd = c.Value
	}

	if passwd != Sha512passwd {
		http.SetCookie(w, &http.Cookie{Name: "password", MaxAge: -1})
		return nil
	}

	if r.Method == "GET" {
		return web.ExecuteTemplate(w, "admin.html", nil)
	} else if r.Method == "POST" {
		r.ParseForm()
		var c uint64

		if new := r.PostForm.Get("new-category"); new != "" {
			nc := &Category{Name: new, Slug: r.PostForm.Get("slug")}
			err := DB.Insert(nc)
			if err != nil {
				return &web.NetError{500, err.Error()}
			}
			c = nc.CategoryID
		} else {
			c, err = strconv.ParseUint(r.PostForm.Get("categoryid"), 10, 64)
			if err != nil {
				return &web.NetError{500, err.Error()}
			}
		}

		j := &Joke{Joke: r.PostForm.Get("joke-submit"), Date: time.Now(), CategoryID: c}
		err = DB.Insert(j)
		if err != nil {
			return &web.NetError{500, err.Error()}
		}
		p, err := strconv.ParseUint(r.PostForm.Get("proposed"), 10, 64)
		if err != nil {
			return &web.NetError{500, err.Error()}
		}
		_, err = DB.Delete(&ProposedJoke{JokeID: p})
		if err != nil {
			return &web.NetError{500, err.Error()}
		}
		http.Redirect(w, r, PathAdmin, http.StatusSeeOther)
	} else {
		return &web.NetError{501, "can't handle verb"}
	}
	return nil
}

func sitemapHandler(w http.ResponseWriter, r *http.Request) *web.NetError {
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(r.URL.Path)))
	return web.ExecuteTemplate(w, "sitemap.html", nil)
}

func main() {
	p := ":8080"
	if len(os.Args) > 1 {
		p = os.Args[1]
	}
	http.HandleFunc(PathJoke, web.ErrorHandler(jokeHandler))
	http.HandleFunc(PathCategory, web.ErrorHandler(categoryHandler))
	http.HandleFunc(PathSubmit, web.ErrorHandler(submitHandler))
	http.HandleFunc(PathAdmin, web.ErrorHandler(adminHandler))
	http.HandleFunc("/sitemap.txt", web.ErrorHandler(sitemapHandler))
	http.Handle("/static/", http.FileServer(http.Dir(".")))
	http.HandleFunc("/", web.ErrorHandler(rootHandler))
	err := http.ListenAndServe(p, nil)
	if err != nil {
		log.Fatal(err)
	}
}
