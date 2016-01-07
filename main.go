// A database for jokes.
package main

import (
	"database/sql"
	"fmt"
	htpl "html/template"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/xojoc/web"
	"gopkg.in/gorp.v1"

	_ "github.com/mattn/go-sqlite3"
)

var (
	DB     *gorp.DbMap
	DBName string = "./jokes.db"
)

type Joke struct {
	JokeID     uint64
	Joke       string
	Date       time.Time
	CategoryID uint64

	Category *Category `db:"-"`
}

type Category struct {
	CategoryID uint64
	Name       string
	Slug       string

	Jokes []*Joke `db:"-"`
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
	DB.CreateTablesIfNotExists()
	web.Pages = htpl.Must(htpl.New("").Funcs(htpl.FuncMap{
		"AllCategories": AllCategories,
		"GetJokes":      GetJokes,
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
	return string([]rune(j.Joke)[:min(20, len(j.Joke))]) + "..." + " - " + SiteTitle
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
	return c.Name + " - " + SiteTitle
}

func AllCategories() ([]*Category, error) {
	var categories []*Category
	_, err := DB.Select(&categories, `select * from Categories order by name`)
	if err != nil {
		return nil, err
	}
	return categories, nil
}

func GetJokes(categoryID uint64, random bool, limit uint) ([]*Joke, error) {
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
	order := ""
	if random {
		order = " order by random() "
	} else {
		order = " order by JokeId desc "
	}
	category := ""
	if categoryID != 0 {
		category = " where CategoryID=" + fmt.Sprint(categoryID) + " "
	}
	var jokes []*Joke

	_, err = DB.Select(&jokes, `select * from Jokes`+category+order+l+`;`)
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
	return web.ExecuteTemplate(w, "joke.html", j)
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
	c.Jokes, err = GetJokes(c.CategoryID, false, 0)
	if err != nil {
		return &web.NetError{500, err.Error()}
	}
	return web.ExecuteTemplate(w, "category.html", c)
}

func staticHandler(w http.ResponseWriter, r *http.Request) *web.NetError {
	w.Header().Add("Cache-Control", "max-age=604800, public")
	http.ServeFile(w, r, "."+r.URL.Path)
	return nil
}

var lastRootTime time.Time
var rootJokes []*Joke

func rootHandler(w http.ResponseWriter, r *http.Request) *web.NetError {
	p := r.URL.Path
	switch {
	case p == "" || p == "/index.html":
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return nil
	case p == "/":
		if time.Now().Sub(lastRootTime) > 1*time.Hour {
			lastRootTime = time.Now()
			var err error
			rootJokes, err = GetJokes(0, true, 10)
			if err != nil {
				return &web.NetError{500, err.Error()}
			}
		}
		return web.ExecuteTemplate(w, "index.html", rootJokes)
	case path.Ext(p) == ".html":
		w.Header().Add("Cache-Control", "max-age=86400, public")
		return web.ExecuteTemplate(w, p[1:], nil)
	case p == "/robots.txt":
		w.Header().Add("Cache-Control", "max-age=86400, public")
		http.ServeFile(w, r, "static/robots.txt")
		return nil
	case p == "/sitemap.txt":
		w.Header().Add("Cache-Control", "max-age=86400, public")
		return web.ExecuteTemplate(w, "sitemap.html", nil)
	default:
		return &web.NetError{404, ""}
	}
}

func main() {
	p := ":8080"
	if len(os.Args) > 1 {
		p = os.Args[1]
	}
	http.HandleFunc(PathJoke, web.ErrorHandler(jokeHandler))
	http.HandleFunc(PathCategory, web.ErrorHandler(categoryHandler))
	http.Handle("/static/", http.FileServer(http.Dir(".")))
	http.HandleFunc("/", web.ErrorHandler(rootHandler))
	err := http.ListenAndServe(p, nil)
	if err != nil {
		log.Fatal(err)
	}
}
