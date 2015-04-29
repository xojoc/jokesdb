package main

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"log"
	"os"
)

var DB *sql.DB

const DBname string = "./jokes.db"

func fileexists(n string) bool {
	_, err := os.Stat(n)
	if os.IsNotExist(err) {
		return false
	}
	return true
}

func init() {
	exists := fileexists(DBname)
	var err error
	DB, err = sql.Open("sqlite3", DBname)
	if err != nil {
		log.Fatal(err)
	}

	if !exists {
		categories := `create table Categories
(CategoryID integer not null,
 Name text not null,
 Slug text not null,
 primary key(CategoryID),
 unique(Slug));`

		jokes := `create table Jokes
(JokeID integer not null,
 Approved boolean not null,
 Joke text not null,
 Reply text not null,
 Likes integer not null,
 Date datetime not null,
 CategoryID integer not null,
 ProposedCategory text not null,
 primary key(JokeID),
 foreign key(CategoryID) references Categories(CategoryID));`

		liked := `create table Liked
(UUID BLOB not null,
 JokeID integer not null,
 date integer not null);`

		for _, s := range [...]string{categories, jokes, liked} {
			_, err = DB.Exec(s)
			if err != nil {
				os.Remove(DBname)
				log.Fatal(err)
			}
		}
	}

	err = DB.Ping()
	if err != nil {
		log.Fatal(err)
	}
}
