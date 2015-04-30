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

var templates = htpl.Must(htpl.New("").Funcs(htpl.FuncMap{"AllCategories": AllCategories, "ProposedJokes": ProposedJokes}).ParseGlob("*.html"))

const (
	PageTitle    = "Barzedette: barzellette"
	Domain       = "http://barzedette.pw"
	Sha512passwd = "4c516647bf061fa36a28fbb09d54e08f0d17b915b3edc5b07b5dc550ba5f8b447143f2660e3b9d77a20479e6a3f2de5e9fa64113ea44024eb9bc9f08d217b413"
)

type Joke struct {
	JokeID     uint64
	Joke       string
	Reply      string
	Likes      uint64
	Date       time.Time
	CategoryID uint64

	Liked bool
}

type Category struct {
	CategoryID uint64
	Name       string
	Slug       string

	Jokes []*Joke
}

type TweetButton struct {
	Text    string
	Via     string
	Related string
	Url     string
	Count   string
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func (j *Joke) AbsUrl() string {
	return Domain + "/barzelletta/" + strconv.FormatUint(j.JokeID, 10)
}
func (j *Joke) Title() string {
	return PageTitle + " | " + j.Joke[:min(10, len(j.Joke))] + "..."
}
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
	return Domain + "/barzellette/" + c.Slug
}

func AllCategories() []*Category {
	rows, err := DB.Query(`select CategoryID, Name, Slug from Categories order by Name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var categories []*Category
	for rows.Next() {
		var c Category
		err := rows.Scan(&c.CategoryID, &c.Name, &c.Slug)
		if err != nil {
			return nil
		}
		categories = append(categories, &c)
	}
	err = rows.Err()
	if err != nil {
		return nil
	}

	return categories
}

func ProposedJokes() []*Joke {
	rows, err := DB.Query(`select rowid, Joke from proposed_jokes;`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var jokes []*Joke
	for rows.Next() {
		var j Joke
		err := rows.Scan(&j.JokeID, &j.Joke)
		if err != nil {
			return nil
		}
		jokes = append(jokes, &j)
	}
	err = rows.Err()
	if err != nil {
		return nil
	}

	return jokes
}

func barzellettaHandler(w http.ResponseWriter, r *http.Request) {
	bidstr := r.URL.Path[len("/barzelletta/"):]
	if bidstr == "" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return
	}
	bid, err := strconv.ParseUint(bidstr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	b := &Joke{}
	err = DB.QueryRow(`select Joke, Reply, Likes, Date from Jokes where JokeID=?;`, bid).Scan(&b.Joke, &b.Reply, &b.Likes, &b.Date)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		return
	}
	b.JokeID = bid
	b.WasLiked(r)

	err = templates.ExecuteTemplate(w, "barzelletta-page.html", b)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func barzelletteHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Path[len("/barzellette/"):]
	if slug == "" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return
	}

	c := &Category{}
	err := DB.QueryRow(`select CategoryID, Name from Categories where Slug=?`, slug).Scan(&c.CategoryID, &c.Name)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		return
	}
	c.Slug = slug

	o := ""
	switch r.URL.Query().Get("orderby") {
	case "newer":
		o = "Date desc;"
	case "older":
		o = "Date asc;"
	default:
		o = "Likes desc;"
	}
	rows, err := DB.Query(`select JokeID,Joke,Reply,Likes from Jokes where CategoryID=? order by `+o, c.CategoryID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		return
	}
	defer rows.Close()
	var jokes []*Joke
	for rows.Next() {
		var j Joke
		err := rows.Scan(&j.JokeID, &j.Joke, &j.Reply, &j.Likes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		j.WasLiked(r)
		jokes = append(jokes, &j)
	}
	err = rows.Err()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	c.Jokes = jokes
	err = templates.ExecuteTemplate(w, "categoria.html", c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

var countBeforePurge = 0

func likeHandler(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	bid, err := strconv.ParseUint(string(body), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var u uuid.UUID
	c, err := r.Cookie("uuid")
	if err != nil {
		u = uuid.NewV4()
		http.SetCookie(w, &http.Cookie{Name: "uuid", Value: u.String(), Expires: time.Now().Add(60 * 24 * time.Hour)})
	} else {
		u, err = uuid.ParseUUID(c.Value)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		placeholder := 0
		err = DB.QueryRow(`SELECT JokeID FROM Liked WHERE UUID=? and JokeID=?;`, u.Bytes(), bid).Scan(&placeholder)
		if err == nil {
			/* already liked. Actually shouldn't happen. */
			return
		}
		/* error */
		if err != sql.ErrNoRows {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = DB.Exec(`INSERT INTO Liked(Uuid, JokeID, date) VALUES(?,?,?);`, u.Bytes(), bid, time.Now().Unix())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "" || r.URL.Path == "/" || r.URL.Path == "/index.html" {
		rows, err := DB.Query(`select JokeID,Joke,Reply,Likes from Jokes order by date limit 20;`)
		if err != nil {
			if err == sql.ErrNoRows {
				http.NotFound(w, r)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}

			return
		}
		defer rows.Close()
		var jokes []*Joke
		for rows.Next() {
			var j Joke
			err := rows.Scan(&j.JokeID, &j.Joke, &j.Reply, &j.Likes)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			j.WasLiked(r)
			jokes = append(jokes, &j)
		}
		err = rows.Err()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = templates.ExecuteTemplate(w, "index.html", jokes)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		return
	}

	p := r.URL.Path[len("/"):]

	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(p)))

	if path.Ext(p) == ".html" {
		err := templates.ExecuteTemplate(w, p, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		f, err := os.Open(p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		io.Copy(w, f)
	}
}

func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		err := templates.ExecuteTemplate(w, "submit.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if r.Method == "POST" {
		r.ParseForm()
		_, err := DB.Exec(`INSERT INTO proposed_jokes VALUES(?);`, r.PostForm.Get("barzelletta"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		err = templates.ExecuteTemplate(w, "submit-success.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

	} else {
		log.Print("can't handle verb")
		http.Error(w, "can't handle verb", http.StatusInternalServerError)
		return
	}
}

func adminHandler(w http.ResponseWriter, r *http.Request) {
	var passwd string
	c, err := r.Cookie("password")
	if err != nil {
		if r.Method == "GET" {
			err = templates.ExecuteTemplate(w, "passwd.html", nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			return
		} else if r.Method == "POST" {
			r.ParseForm()
			passwd = fmt.Sprintf("%x", sha512.Sum512([]byte(r.PostForm.Get("password"))))
			http.SetCookie(w, &http.Cookie{Name: "password", Value: passwd, Expires: time.Now().Add(60 * 24 * time.Hour)})
			return
		} else {
			http.Error(w, "can't handle verb", http.StatusInternalServerError)
			return
		}
	} else {
		passwd = c.Value
	}

	if passwd != Sha512passwd {
		http.SetCookie(w, &http.Cookie{Name: "password", MaxAge: -1})
		return
	}

	if r.Method == "GET" {
		err := templates.ExecuteTemplate(w, "admin.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if r.Method == "POST" {
		r.ParseForm()
		var c uint64

		if new := r.PostForm.Get("nuova-categoria"); new != "" {
			res, err := DB.Exec(`INSERT INTO Categories(Name, Slug) Values(?,?);`, new, r.PostForm.Get("slug"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			c64, err := res.LastInsertId()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			c = uint64(c64)

		} else {
			c, err = strconv.ParseUint(r.PostForm.Get("categoria"), 10, 64)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		_, err = DB.Exec(`INSERT INTO Jokes(Joke,Reply,Likes,Date,CategoryID) VALUES(?,?,?,?,?);`, r.PostForm.Get("barzelletta"), r.PostForm.Get("risposta"), 0, time.Now(), c)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		p, err := strconv.ParseUint(r.PostForm.Get("proposed"), 10, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, err = DB.Exec(`DELETE FROM proposed_jokes WHERE rowid=?;`, p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

	} else {
		http.Error(w, "can't handle verb", http.StatusInternalServerError)
		return
	}
}

func main() {
	p := ":8080"
	if len(os.Args) > 1 {
		p = os.Args[1]
	}
	log.SetFlags(log.Lshortfile)
	http.HandleFunc("/barzelletta/", barzellettaHandler)
	http.HandleFunc("/barzellette/", barzelletteHandler)
	http.HandleFunc("/like", likeHandler)
	http.HandleFunc("/submit", submitHandler)
	http.HandleFunc("/admin", adminHandler)
	http.HandleFunc("/", rootHandler)
	http.ListenAndServe(p, nil)
}
