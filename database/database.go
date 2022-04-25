package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/url"
	"os"
	"time"
	"math/rand"

	"cerca/util"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	db *sql.DB
}

func InitDB(filepath string) DB {
	if _, err := os.Stat(filepath); errors.Is(err, os.ErrNotExist) {
		file, err := os.Create(filepath)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
	}

	db, err := sql.Open("sqlite3", filepath)
	util.Check(err, "opening sqlite3 database at %s", filepath)
	if db == nil {
		log.Fatalln("db is nil")
	}
	createTables(db)
	return DB{db}
}

func createTables(db *sql.DB) {
	// create the table if it doesn't exist
	queries := []string{
		/* used for versioning migrations */
		`
  CREATE TABLE IF NOT EXISTS meta (
    schemaversion INTEGER NOT NULL
  );
  `,
		`
  CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    passwordhash TEXT NOT NULL,
	gardenids TEXT NOT NULL,
	lastrefresh DATE,
	invites INTEGER DEFAULT 0,
	email TEXT
  );
  `,
		`
  CREATE TABLE IF NOT EXISTS nonces (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    nonce TEXT NOT NULL UNIQUE
  );
  `,
		`
  CREATE TABLE IF NOT EXISTS pubkeys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pubkey TEXT NOT NULL UNIQUE,
    userid integer NOT NULL UNIQUE,
    FOREIGN KEY (userid) REFERENCES users(id)
  );
  `,
		`
  CREATE TABLE IF NOT EXISTS registrations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    userid INTEGER,
    host STRING,
    link STRING,
    time DATE,
    FOREIGN KEY (userid) REFERENCES users(id)
  );
  `,

		/* also known as forum categories; buckets of threads */
		`
  CREATE TABLE IF NOT EXISTS topics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT
  );
  `,
		/* thread link structure: <domain>.<tld>/seed/<id>/[<blurb>] */
		`
  CREATE TABLE IF NOT EXISTS threads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    publishtime DATE,
    topicid INTEGER,
    authorid INTEGER,
    FOREIGN KEY(topicid) REFERENCES topics(id),
    FOREIGN KEY(authorid) REFERENCES users(id)
  );
  `,
		`
  CREATE TABLE IF NOT EXISTS posts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    content TEXT NOT NULL,
    publishtime DATE,
    lastedit DATE,
    authorid INTEGER,
    threadid INTEGER,
    FOREIGN KEY(authorid) REFERENCES users(id),
    FOREIGN KEY(threadid) REFERENCES threads(id)
  );
  `,
  `CREATE TABLE IF NOT EXISTS likes (
	  id INTEGER PRIMARY KEY AUTOINCREMENT,
	  date DATE,
	  userid INTEGER,
	  threadid INTEGER
  )`,
  `CREATE TABLE IF NOT EXISTS invites (
	  id INTEGER PRIMARY KEY AUTOINCREMENT,
	  code STRING NOT NULL UNIQUE,
	  authorid INTEGER,
	  recipientid INTEGER
  )`}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			log.Fatalln(util.Eout(err, "creating database table %s", query))
		}
	}
}

/* goal for 2021-12-26
* create thread
* create post
* get thread
* + html render of begotten thread
 */

/* goal for 2021-12-28
* in browser: reply on a thread
* in browser: create a new thread
 */
func (d DB) Exec(stmt string, args ...interface{}) (sql.Result, error) {
	return d.db.Exec(stmt, args...)
}

func (d DB) CreateThread(title, content string, authorid, topicid int) (int, error) {
	ed := util.Describe("create thread")
	// create the new thread in a transaction spanning two statements
	tx, err := d.db.BeginTx(context.Background(), &sql.TxOptions{}) // proper tx options?
	ed.Check(err, "start transaction")
	// first, create the new thread
	publish := time.Now()
	threadStmt := `INSERT INTO threads (title, publishtime, topicid, authorid) VALUES (?, ?, ?, ?)
  RETURNING id`
	replyStmt := `INSERT INTO posts (content, publishtime, threadid, authorid) VALUES (?, ?, ?, ?)`
	var threadid int
	err = tx.QueryRow(threadStmt, title, publish, topicid, authorid).Scan(&threadid)
	if err = ed.Eout(err, "add thread %s by %d in topic %d", title, authorid, topicid); err != nil {
		_ = tx.Rollback()
		log.Println(err, "rolling back")
		return -1, err
	}
	// then add the content as the first reply to the thread
	_, err = tx.Exec(replyStmt, content, publish, threadid, authorid)
	if err = ed.Eout(err, "add initial reply for thread %d", threadid); err != nil {
		_ = tx.Rollback()
		log.Println(err, "rolling back")
		return -1, err
	}
	err = tx.Commit()
	ed.Check(err, "commit transaction")

	d.SetLike(authorid, threadid, true)

	// this doesn't seem to work?
	stmt := fmt.Sprintf(`UPDATE users SET gardenids = "%d," || gardenids WHERE id = %d`, threadid, authorid)
	_, err = d.Exec(stmt)
	util.Check(err, "adding thread %d to garden of user %s", threadid, authorid)

	// finally return the id of the created thread, so we can do a friendly redirect
	return threadid, nil
}

// c.f.
// https://medium.com/aubergine-solutions/how-i-handled-null-possible-values-from-database-rows-in-golang-521fb0ee267
// type NullTime sql.NullTime
type Post struct {
	ID          int
	ThreadTitle string
	Content     template.HTML
	Author      string
	AuthorID    int
	Publish     time.Time
	LastEdit    sql.NullTime // TODO: handle json marshalling with custom type
}

func (d DB) DeleteThread() {}
func (d DB) MoveThread()   {}

// TODO(2021-12-28): return error if non-existent thread
func (d DB) GetThread(threadid int) []Post {
	// TODO: make edit work if no edit timestamp detected e.g.
	// (sql: Scan error on column index 3, name "lastedit": unsupported Scan, storing driver.Value type <nil> into type
	// *time.Time)

	// join with:
	//    users table to get user name
	//    threads table to get thread title
	query := `
  SELECT p.id, t.title, content, u.name, p.authorid, p.publishtime, p.lastedit
  FROM posts p 
  INNER JOIN users u ON u.id = p.authorid 
  INNER JOIN threads t ON t.id = p.threadid
  WHERE threadid = ? 
  ORDER BY p.publishtime
  `
	stmt, err := d.db.Prepare(query)
	util.Check(err, "get thread: prepare query")
	defer stmt.Close()

	rows, err := stmt.Query(threadid)
	util.Check(err, "get thread: query")
	defer rows.Close()

	var data Post
	var posts []Post
	for rows.Next() {
		if err := rows.Scan(&data.ID, &data.ThreadTitle, &data.Content, &data.Author, &data.AuthorID, &data.Publish, &data.LastEdit); err != nil {
			log.Fatalln(util.Eout(err, "get data for thread %d", threadid))
		}
		posts = append(posts, data)
	}
	return posts
}

func (d DB) GetPost(postid int) (Post, error) {
	stmt := `
  SELECT p.id, t.title, content, u.name, p.authorid, p.publishtime, p.lastedit
  FROM posts p 
  INNER JOIN users u ON u.id = p.authorid 
  INNER JOIN threads t ON t.id = p.threadid
  WHERE p.id = ?
  `
	var data Post
	err := d.db.QueryRow(stmt, postid).Scan(&data.ID, &data.ThreadTitle, &data.Content, &data.Author, &data.AuthorID, &data.Publish, &data.LastEdit)
	err = util.Eout(err, "get data for thread %d", postid)
	return data, err
}

type Thread struct {
	Title     string
	Author    string
	Slug      string
	ID        int
	PostCount int
	UserPostCount int
	Publish   time.Time
}

// get a list of threads
func (d DB) ListThreads(sortByPost bool) []Thread {
	query := `
  SELECT count(t.id), t.title, t.id, u.name FROM threads t
  INNER JOIN users u on u.id = t.authorid
  INNER JOIN posts p ON t.id = p.threadid
  GROUP BY t.id
  %s
  `
	orderBy := `ORDER BY t.publishtime DESC`
	// get a list of threads by ordering them based on most recent post
	if sortByPost {
		orderBy = `ORDER BY max(p.id) DESC`
	}
	query = fmt.Sprintf(query, orderBy)

	stmt, err := d.db.Prepare(query)
	util.Check(err, "list threads: prepare query")
	defer stmt.Close()

	rows, err := stmt.Query()
	util.Check(err, "list threads: query")
	defer rows.Close()

	var postCount int
	var data Thread
	var threads []Thread
	for rows.Next() {
		if err := rows.Scan(&postCount, &data.Title, &data.ID, &data.Author); err != nil {
			log.Fatalln(util.Eout(err, "list threads: read in data via scan"))
		}
		data.Slug = util.GetThreadSlug(data.ID, data.Title, postCount)
		data.PostCount = postCount
		threads = append(threads, data)
	}
	return threads
}

// get a list of threads
func (d DB) ListThreadsUser(userid int) []Thread {
// 	query := fmt.Sprintf(`
//   SELECT count(t.id), t.title, t.id, u.name FROM threads t
//   INNER JOIN users u on u.id = t.authorid
//   INNER JOIN posts p ON t.id = p.threadid
//   WHERE t.id IN (
// 	  WITH split(id, str) AS (
// 		  SELECT '', gardenids||',' FROM users WHERE id = %d
// 		  UNION ALL SELECT
// 		  substr(str, 0, instr(str, ',')),
// 		  substr(str, instr(str, ',')+1)
// 		  FROM split WHERE str!=''
// 	  ) SELECT cast(id AS INTEGER) FROM split WHERE id!=''
//   )
//   GROUP BY t.id
// `, userid)

  	query := fmt.Sprintf(`
		WITH split(id, str, ndx) AS (
			SELECT '', gardenids||',',0 FROM users WHERE id = %d
			UNION ALL SELECT
			substr(str, 0, instr(str, ',')),
			substr(str, instr(str, ',')+1),
			ndx+1
			FROM split WHERE str!=''
		) SELECT count(t.id), t.title, t.id, u.name FROM split
		INNER JOIN threads t ON t.id = split.id
		INNER JOIN users u ON u.id = t.authorid
		INNER JOIN posts p ON t.id = p.threadid
		WHERE split.id != ''
		GROUP BY t.id
		ORDER BY split.ndx
	`, userid)

	stmt, err := d.db.Prepare(query)
	util.Check(err, "list threads: prepare query")
	defer stmt.Close()

	rows, err := stmt.Query()
	util.Check(err, "list threads: query")
	defer rows.Close()

	var postCount int
	var data Thread
	var threads []Thread
	for rows.Next() {
		if err := rows.Scan(&postCount, &data.Title, &data.ID, &data.Author); err != nil {
			log.Fatalln(util.Eout(err, "list threads: read in data via scan"))
		}
		data.Slug = util.GetThreadSlug(data.ID, data.Title, postCount)
		data.PostCount = postCount
		threads = append(threads, data)
	}
	return threads
}

// func (d DB) GetCultivatedThreads(userid int) []Thread {
	
// }

func (d DB) RefreshThreads(userid int) {

	threads := d.ListThreads(true)
	// log.Println(threads)

	// newids := []int{}
	newids := d.GetLikes(userid)

	for i := 0; i < 6; i++ {
		index := rand.Intn(len(threads))
		
		// add ID of new thread to the garden
		newids = append(newids, threads[index].ID)

		// remove element from array to prevent duplicates
		threads = append(threads[:index], threads[index+1:]...)
	}

	gardenids := util.ArrayToString(newids, ",")
	lastrefresh := time.Now()

	// stmt := fmt.Sprintf(`UPDATE users SET gardenids = "%s",  WHERE id = %d`, util.ArrayToString(newids, ","), userid)
	stmt := `UPDATE users SET gardenids = ?, lastrefresh = ? WHERE id = ?`
	_, err := d.Exec(stmt, gardenids, lastrefresh, userid)
	util.Check(err, "refresh threads for user %d", userid)

	
	// stmt = fmt.Sprintf(`UPDATE `)
}

func (d DB) SetLike(userid int, threadid int, value bool) {
	stmt := `SELECT 1 FROM likes WHERE userid = ? AND threadid = ?`
	exists, _ := d.existsQuery(stmt, userid, threadid)

	if exists && !value {
		stmt := `DELETE FROM likes WHERE userid = ? AND threadid = ?`
		_, err := d.Exec(stmt, userid, threadid)

		util.Check(err, "remove like for user %d from thread %d", userid, threadid)
	} else if !exists && value {
		stmt := `INSERT INTO likes (date, userid, threadid) VALUES (?, ?, ?)`
		_, err := d.Exec(stmt, time.Now(), userid, threadid)

		util.Check(err, "add like for user %d from thread %d", userid, threadid)
	}
}

// type Like struct {
// 	Title     string
// 	Author    string
// 	Slug      string
// 	ID        int
// 	PostCount int
// 	Date   time.Time
// }

type Invite struct {
	Code          string
	Author        string
	Recipient     string
	Used		  bool

	AuthorID      int
	RecipientID   int
}

func (d DB) GenerateNewInvite(userid int) string {
	var code string
	var exists bool
	var err error

	BASE36 := "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

	// in the extremely rare chance that it generates a code that already exists
	// it will regenerate until it generates a valid new invite code
	for code == "" || exists {
		code = ""
		for i := 0; i < 7; i++ {
			code = code + string(BASE36[rand.Intn(len(BASE36))])
		}

		exists, err = d.CheckInviteCodeExists(code)
		util.Check(err, "generating invite code")

		// fmt.Println(code)
	}

	stmt := `INSERT INTO invites (code, authorid) VALUES (?, ?)`
	_, err = d.Exec(stmt, code, userid)
	util.Check(err, "issuing invite code %s for user %d", code, userid)

	return code
}

func (d DB) ListInvites(userid int) []Invite {
	query := fmt.Sprintf(`
		SELECT code, IFNULL(u.name, "") FROM invites
		LEFT JOIN users u ON u.id = recipientid
		WHERE authorid = %d
	`, userid)

	stmt, err := d.db.Prepare(query)
	util.Check(err, "list invites: prepare query")
	defer stmt.Close()

	rows, err := stmt.Query()
	util.Check(err, "list invites: query")
	defer rows.Close()

	var data Invite
	var invites []Invite
	for rows.Next() {
		if err := rows.Scan(&data.Code, &data.Recipient); err != nil {
			log.Fatalln(util.Eout(err, "list invites: read in data via scan"))
		}
		
		// has been used if we find a username for the recipient
		data.Used = (data.Recipient != "")

		invites = append(invites, data)
	}
	return invites
}

func (d DB) GetInvite(code string) (Invite, error) {
	stmt := fmt.Sprintf(`
		SELECT IFNULL(author.name, ""), IFNULL(recipient.name, ""), IFNULL(authorid, -1), IFNULL(recipientid, -1) FROM invites
		LEFT JOIN users author ON author.id = authorid
		LEFT JOIN users recipient ON recipient.id = recipientid
		WHERE code = "%s"
	`, code)

	var data Invite
	err := d.db.QueryRow(stmt).Scan(&data.Author, &data.Recipient, &data.AuthorID, &data.RecipientID)
	if err != nil {
		return data, util.Eout(err, "get invite")
	}

	data.Used = (data.Recipient != "")
	data.Code = code

	return data, nil
}

func (d DB) GetParticipatedThreads(userid int) []Thread {
	query := fmt.Sprintf(`
		SELECT
			count(t.id),
			(SELECT count(t2.id) FROM threads t2 INNER JOIN posts p2 ON p2.threadid = t2.id WHERE t2.id = p.threadid),
			t.title,
			t.id,
			u.name
		FROM posts p
		LEFT OUTER JOIN threads t ON t.id = p.threadid
		INNER JOIN users u ON u.id = t.authorid
		WHERE p.authorid = %d
		GROUP BY t.id
		ORDER BY MAX(p.publishtime);
	`, userid)

	stmt, err := d.db.Prepare(query)
	util.Check(err, "get participated threads: prepare query")
	defer stmt.Close()

	rows, err := stmt.Query()
	util.Check(err, "get participated threads: query")
	defer rows.Close()

	var postCount int
	var userPostCount int
	var data Thread
	var threads []Thread
	for rows.Next() {
		if err := rows.Scan(&userPostCount, &postCount, &data.Title, &data.ID, &data.Author); err != nil {
			log.Fatalln(util.Eout(err, "get participated threads: read in data via scan"))
		}
		data.Slug = util.GetThreadSlug(data.ID, data.Title, postCount)
		data.PostCount = postCount
		data.UserPostCount = userPostCount

		// if you are the author of the thread and there is only one post
		// don't count it as a participated thread??
		// if !(postCount == 1 && data.ID == userid) {
			threads = append(threads, data)
		// }
	}
	return threads
}

func (d DB) GetLikes(userid int) []int {
	query := fmt.Sprintf(`
		SELECT threadid FROM likes
		WHERE userid = %d
		GROUP BY id
	`, userid)

	stmt, err := d.db.Prepare(query)
	util.Check(err, "getting likes: prepare query")
	defer stmt.Close()

	rows, err := stmt.Query()
	util.Check(err, "getting likes: query")
	defer rows.Close()

	var threadid int
	var likes []int
	for rows.Next() {
		if err := rows.Scan(&threadid); err != nil {
			log.Fatalln(util.Eout(err, "getting likes: read in data via scan"))
		}
		
		likes = append(likes, threadid)
	}
	return likes
}

func (d DB) GetNumberOfLikesThread(threadid int) int {
	query := fmt.Sprintf(`
		SELECT count(*) FROM likes
		WHERE threadid = %d
	`, threadid)

	// likes, err := d.Exec(stmt, threadid)
	// util.Check(err, "edit post %d", postid)

	// fmt.Printf(likes.Next())

	stmt, err := d.db.Prepare(query)
	util.Check(err, "getting # of likes: prepare query")
	defer stmt.Close()

	rows, err := stmt.Query()
	util.Check(err, "getting # of likes: query")
	defer rows.Close()

	rows.Next()
	var likes int
	rows.Scan(&likes)

	// fmt.Println(likes)

	// var threadid int
	// var likes []int
	// for rows.Next() {
	// 	if err := rows.Scan(&threadid); err != nil {
	// 		log.Fatalln(util.Eout(err, "getting likes: read in data via scan"))
	// 	}
		
	// 	likes = append(likes, threadid)
	// }
	return likes
}

func (d DB) AddPost(content string, threadid, authorid int) (postID int) {
	stmt := `INSERT INTO posts (content, publishtime, threadid, authorid) VALUES (?, ?, ?, ?) RETURNING id`
	publish := time.Now()
	err := d.db.QueryRow(stmt, content, publish, threadid, authorid).Scan(&postID)
	util.Check(err, "add post to thread %d (author %d)", threadid, authorid)

	d.SetLike(authorid, threadid, false)

	// splice the id of thread (if it exists) out of comma-separated gardenids
	stmt = fmt.Sprintf(`
	UPDATE users SET gardenids = (
		WITH split(id, str) AS (
			SELECT '', gardenids||',' FROM users WHERE id = %d
			UNION ALL SELECT
			substr(str, 0, instr(str, ',')),
			substr(str, instr(str, ',')+1)
			FROM split WHERE str!=''
		) SELECT GROUP_CONCAT(id, ",") FROM split
		WHERE id != "%d" AND id != ""
	) WHERE id = %d;
    `, authorid, threadid, authorid)
	_, err = d.Exec(stmt)
	util.Check(err, "removing thread %d from gardenids of user %d", threadid, authorid)

	return
}

func (d DB) EditPost(content string, postid int) {
	stmt := `UPDATE posts set content = ?, lastedit = ? WHERE id = ?`
	edit := time.Now()
	_, err := d.Exec(stmt, content, edit, postid)
	util.Check(err, "edit post %d", postid)
}

func (d DB) DeletePost(postid int) error {
	stmt := `DELETE FROM posts WHERE id = ?`
	_, err := d.Exec(stmt, postid)
	return util.Eout(err, "deleting post %d", postid)
}

func (d DB) GetLastRefresh(userid int) (time.Time, error) {
	stmt := `SELECT lastrefresh FROM users where id = ?`
	var lastrefresh time.Time
	err := d.db.QueryRow(stmt, userid).Scan(&lastrefresh)
	if err != nil {
		return time.UnixMicro(0), util.Eout(err, "get last refresh")
	}
	return lastrefresh, nil
	// return lastrefresh, nil
}

func (d DB) CreateTopic(title, description string) {
	stmt := `INSERT INTO topics (name, description) VALUES (?, ?)`
	_, err := d.Exec(stmt, title, description)
	util.Check(err, "creating topic %s", title)
}

func (d DB) UpdateTopicName(topicid int, newname string) {
	stmt := `UPDATE topics SET name = ? WHERE id = ?`
	_, err := d.Exec(stmt, newname, topicid)
	util.Check(err, "changing topic %d's name to %s", topicid, newname)
}

func (d DB) UpdateTopicDescription(topicid int, newdesc string) {
	stmt := `UPDATE topics SET description = ? WHERE id = ?`
	_, err := d.Exec(stmt, newdesc, topicid)
	util.Check(err, "changing topic %d's description to %s", topicid, newdesc)
}

func (d DB) DeleteTopic(topicid int) {
	stmt := `DELETE FROM topics WHERE id = ?`
	_, err := d.Exec(stmt, topicid)
	util.Check(err, "deleting topic %d", topicid)
}

func (d DB) CreateUser(name, hash, email, code string) (int, error) {
	stmt := `INSERT INTO users (name, passwordhash, email) VALUES (?, ?, ?) RETURNING id`
	var userid int
	err := d.db.QueryRow(stmt, name, hash, email).Scan(&userid)
	if err != nil {
		return -1, util.Eout(err, "creating user %s", name)
	}

	// register that invite code has been used
	stmt = `UPDATE invites SET recipientid = ? WHERE code = ?`
	_, err = d.Exec(stmt, userid, code)
	util.Check(err, "setting invite code %s as having been used by user %d", code, userid)

	return userid, nil
}

func (d DB) GetUserID(name string) (int, error) {
	stmt := `SELECT id FROM users where name = ?`
	var userid int
	err := d.db.QueryRow(stmt, name).Scan(&userid)
	if err != nil {
		return -1, util.Eout(err, "get user id")
	}
	return userid, nil
}

func (d DB) GetPasswordHash(username string) (string, int, error) {
	stmt := `SELECT passwordhash, id FROM users where name = ?`
	var hash string
	var userid int
	err := d.db.QueryRow(stmt, username).Scan(&hash, &userid)
	if err != nil {
		return "", -1, util.Eout(err, "get password hash")
	}
	return hash, userid, nil
}

func (d DB) existsQuery(substmt string, args ...interface{}) (bool, error) {
	stmt := fmt.Sprintf(`SELECT exists (%s)`, substmt)
	var exists bool
	err := d.db.QueryRow(stmt, args...).Scan(&exists)
	if err != nil {
		return false, util.Eout(err, "exists: %s", substmt)
	}
	return exists, nil
}

func (d DB) CheckUserExists(userid int) (bool, error) {
	stmt := `SELECT 1 FROM users WHERE id = ?`
	return d.existsQuery(stmt, userid)
}

func (d DB) CheckInviteCodeExists(code string) (bool, error) {
	// stmt := `SELECT 1 FROM invites WHERE code = ?`
	stmt := fmt.Sprintf(`SELECT 1 FROM invites WHERE code = "%s"`, code)
	fmt.Println(stmt)
	return d.existsQuery(stmt)
}

func (d DB) CheckNonceExists(nonce string) (bool, error) {
	stmt := `SELECT 1 FROM nonces WHERE nonce = ?`
	return d.existsQuery(stmt, nonce)
}

func (d DB) CheckUsernameExists(username string) (bool, error) {
	stmt := `SELECT 1 FROM users WHERE name = ?`
	return d.existsQuery(stmt, username)
}

func (d DB) UpdateUserName(userid int, newname string) {
	stmt := `UPDATE users SET name = ? WHERE id = ?`
	_, err := d.Exec(stmt, newname, userid)
	util.Check(err, "changing user %d's name to %s", userid, newname)
}

func (d DB) UpdateUserPasswordHash(userid int, newhash string) {
	stmt := `UPDATE users SET passwordhash = ? WHERE id = ?`
	_, err := d.Exec(stmt, newhash, userid)
	util.Check(err, "changing user %d's description to %s", userid, newhash)
}

func (d DB) DeleteUser(userid int) {
	stmt := `DELETE FROM users WHERE id = ?`
	_, err := d.Exec(stmt, userid)
	util.Check(err, "deleting user %d", userid)
}

func (d DB) AddPubkey(userid int, pubkey string) error {
	ed := util.Describe("add pubkey")
	// TODO (2022-02-03): the insertion order is wrong >.<
	stmt := `INSERT INTO pubkeys (pubkey, userid) VALUES (?, ?)`
	_, err := d.Exec(stmt, userid, pubkey)
	if err = ed.Eout(err, "inserting record"); err != nil {
		return err
	}
	return nil
}

func (d DB) GetPubkey(userid int) (pubkey string, err error) {
	ed := util.Describe("get pubkey")
	// due to a mishap in the query in AddPubkey the column `pubkey` contains the userid
	// and the column `userid` contains the pubkey
	// :'))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))
	// TODO (2022-02-03): when we have migration logic, fix this mishap
	stmt := `SELECT userid from pubkeys where pubkey = ?`
	err = d.db.QueryRow(stmt, userid).Scan(&pubkey)
	err = ed.Eout(err, "query & scan")
	return
}

// TODO (2022-02-04): extend mrv verification code length and reuse nonce scheme to fix registration bug?
func (d DB) AddNonce(nonce string) error {
	ed := util.Describe("add nonce")
	stmt := `INSERT INTO nonces (nonce) VALUES (?)`
	_, err := d.Exec(stmt, nonce)
	if err != nil {
		return ed.Eout(err, "insert nonce")
	}
	return nil
}

func (d DB) AddRegistration(userid int, verificationLink string) error {
	ed := util.Describe("add registration")
	stmt := `INSERT INTO registrations (userid, host, link, time) VALUES (?, ?, ?, ?)`
	t := time.Now()
	u, err := url.Parse(verificationLink)
	if err = ed.Eout(err, "parse url"); err != nil {
		return err
	}
	_, err = d.Exec(stmt, userid, u.Host, verificationLink, t)
	if err = ed.Eout(err, "add registration"); err != nil {
		return err
	}
	return nil
}
