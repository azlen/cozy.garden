package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	// "net/url"
	"strings"
	"strconv"
	"time"
	"os"
	"math/rand"

	"cerca/crypto"
	"cerca/database"
	cercaHTML "cerca/html"
	"cerca/server/session"
	"cerca/util"

	"github.com/carlmjohnson/requests"
	
	"reflect"
)

/* TODO (2022-01-03): include csrf token via gorilla, or w/e, when rendering */

type TemplateData struct {
	Data       interface{}
	QuickNav   bool
	LoggedIn   bool // TODO (2022-01-09): put this in a middleware || template function or sth?
	LoggedInID int
	Title      string
}

type PasswordResetData struct {
	Action       string
	Username     string
	Payload      string
}

type IndexData struct {
	Threads          []database.Thread
	Likes            []int
}

type GenericMessageData struct {
	Title       string
	Message     string
	LinkMessage string
	Link        string
	LinkText    string
}

type RegisterData struct {
	VerificationCode string
	ErrorMessage     string
	Invite		     database.Invite
}

type RegisterSuccessData struct {
	Keypair string
}

type LoginData struct {
	FailedAttempt bool
}

type ThreadData struct {
	Title        string
	Posts        []database.Post
	ThreadURL    string
	Likes	     int
	Placeholder  string
	Fragment	 string
	Open		 string
}

type InviteData struct {
	Invites   []database.Invite
	Available int
}

type RequestHandler struct {
	db        *database.DB
	session   *session.Session
	// allowlist []string // allowlist of domains valid for forum registration
}

var developing bool
// var refreshinterval = 3 // hours
var enoian []string

func dump(err error) {
	if developing {
		fmt.Println(err)
	}
}

func readLines(location string) []string {
	// fmt.Println("READ LINES")
	ed := util.Describe("read lines")
	data, err := os.ReadFile(location)
	ed.Check(err, "read file")
	list := strings.Split(strings.TrimSpace(string(data)), "\n")
	var processed []string
	for _, line := range list {
		// allow for commenting out lines
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		
		processed = append(processed, line)
	}
	// fmt.Println(string(data))
	return processed
}

// returns true if logged in, and the userid of the logged in user.
// returns false (and userid set to -1) if not logged in
func (h RequestHandler) IsLoggedIn(req *http.Request) (bool, int) {
	ed := util.Describe("IsLoggedIn")
	userid, err := h.session.Get(req)
	err = ed.Eout(err, "getting userid from session cookie")
	if err != nil {
		dump(err)
		return false, -1
	}

	// make sure the user from the cookie actually exists
	userExists, err := h.db.CheckUserExists(userid)
	if err != nil {
		dump(ed.Eout(err, "check userid in db"))
		return false, -1
	} else if !userExists {
		return false, -1
	}
	return true, userid
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}

func interfaceToInt64(x interface{}) int64 {
	switch x := x.(type) {   // This is a type switch.
    case int64:
        return x        // All done if we got an int64.
    case int:
        return int64(x) // This uses a conversion from int to int64
    case string:
		out, _ := strconv.ParseInt(x, 10, 64)
        return out
    default:
		return 0
        // return 0, fmt.Errorf("type %T not supported", t)
    }
}

var (
	templateFuncs = template.FuncMap{
		"formatDateTime": func(t time.Time) string {
			return t.Format("2006-01-02 15:04:05")
		},
		"formatDateTimeRFC3339": func(t time.Time) string {
			return t.Format(time.RFC3339Nano)
		},
		"formatDate": func(t time.Time) string {
			return t.Format("2006-01-02")
		},
		"formatDateTimeAsNumber": func(t time.Time) int64 {
			return t.UnixMilli()
		},
		"timeNow": func() time.Time {
			return time.Now()
		},
		"formatDateRelative": func(t time.Time) string {
			diff := time.Since(t)
			if diff < time.Hour*24 {
				return "today"
			} else if diff >= time.Hour*24 && diff < time.Hour*48 {
				return "yesterday"
			}
			return t.Format("2006-01-02")
		},
		"chladniUrl": func(numPosts int) string {
			return fmt.Sprintf("/assets/chladni/%d.png", min(numPosts * 100, 20000))
		},
		"add": func(a int, b int) int {
			return a + b
		},
		"minus": func(a interface{}, b interface{}) int64 {
			a64 := interfaceToInt64(a)
			b64 := interfaceToInt64(b)
			return a64 - b64
		},
		"multiply": func(a interface{}, b interface{}) int64 {
			a64 := interfaceToInt64(a)
			b64 := interfaceToInt64(b)
			return a64 * b64
		},
		"iterate": func(count *int) []int {
            var i int
            var Items []int
            for i = 0; i < (*count); i++ {
                Items = append(Items, i)
            }
            return Items
        },
		"intToString": func(num int) string {
			return strconv.Itoa(num)
		},
		"float": func(num int) float64 {
			return float64(num)
		},
		"divide": func(a, b float64) float64 {
			return a / b
		},
		"avail": func(name string, data interface{}) bool {
			v := reflect.ValueOf(data)
			if v.Kind() == reflect.Ptr {
				v = v.Elem()
			}
			if v.Kind() != reflect.Struct {
				return false
			}
			return v.FieldByName(name).IsValid()
		},
	}

	templates = template.Must(generateTemplates())
)

func generateTemplates() (*template.Template, error) {
	views := []string{
		"about",
		"community",
		"footer",
		"generic-message",
		"head",
		"index",
		"login",
		"login-component",
		"new-thread",
		"register",
		"register-success",
		"thread",
		"password-reset",
		"landing",
		"invite",
	}

	rootTemplate := template.New("root")

	for _, view := range views {
		newTemplate, err := rootTemplate.Funcs(templateFuncs).ParseFS(cercaHTML.Templates, fmt.Sprintf("%s.html", view))
		if err != nil {
			return nil, fmt.Errorf("could not get files: %w", err)
		}
		rootTemplate = newTemplate
	}

	return rootTemplate, nil
}

func (h RequestHandler) renderView(res http.ResponseWriter, viewName string, data TemplateData) {
	if data.Title == "" {
		data.Title = strings.ReplaceAll(viewName, "-", " ")
	}

	view := fmt.Sprintf("%s.html", viewName)
	if err := templates.ExecuteTemplate(res, view, data); err != nil {
		util.Check(err, "rendering %q view", view)
	}
}

func (h RequestHandler) ThreadRoute(res http.ResponseWriter, req *http.Request) {
	threadid, ok := util.GetURLPortion(req, 2)
	loggedIn, userid := h.IsLoggedIn(req)

	if !ok {
		data := GenericMessageData{
			Title:   "Thread not found",
			Message: "The thread does not exist (anymore?)",
		}
		h.renderView(res, "generic-message", TemplateData{Data: data, LoggedIn: loggedIn})
		return
	}

	if req.Method == "POST" && loggedIn {
		// handle POST (=> add a reply, then show the thread)
		content := req.PostFormValue("content")
		action := req.PostFormValue("action")

		if action == "submission" {
			// TODO (2022-01-09): make sure rendered content won't be empty after sanitizing:
			// * run sanitize step && strings.TrimSpace and check length **before** doing AddPost
			// TODO(2022-01-09): send errors back to thread's posting view
			_ = h.db.AddPost(content, threadid, userid)
			// we want to effectively redirect to <#posts+1> to mark the thread as read in the thread index
			// TODO(2022-01-30): find a solution for either:
			// * scrolling to thread bottom (and maintaining the same slug, important for visited state in browser)
			// * passing data to signal "your post was successfully added" (w/o impacting visited state / url)
			posts := h.db.GetThread(threadid)
			newSlug := util.GetThreadSlug(threadid, posts[0].ThreadTitle, len(posts))
			http.Redirect(res, req, newSlug, http.StatusFound)
		} else if action == "comment" {
			postid, err := strconv.Atoi(req.PostFormValue("post_id"))

			if err != nil {
				fmt.Println(err)

				data := GenericMessageData{
					Title:   "Error posting comment",
					Message: "The data received appears corrupted or tampered with, maybe try again?",
				}
				h.renderView(res, "generic-message", TemplateData{Data: data, LoggedIn: loggedIn})

				return
			}

			commentid := h.db.AddComment(content, threadid, userid, postid)

			fmt.Println(req.URL)
			http.Redirect(res, req, fmt.Sprintf("%s#%d", req.URL.Path, commentid), http.StatusFound)
		}
		
		return
	}
	// TODO (2022-01-07):
	// * handle error
	thread := h.db.GetThread(threadid)

	if len(thread) == 0 {
		title := "Thread not found"
		data := GenericMessageData{
			Title:   title,
			Message: "This thread does not exist (anymore?)",
		}
		h.renderView(res, "generic-message", TemplateData{Data: data, LoggedIn: loggedIn})
		return
	}

	// markdownize content (but not title)
	for i, post := range thread {
		thread[i].Content = util.Markup(post.Content)

		comments := thread[i].Comments
		for j, comment := range comments {
			comments[j].Content = util.Markup(comment.Content)
		}
	}
	
	likes := h.db.GetNumberOfLikesThread(threadid)
	placeholder := enoian[rand.Intn(len(enoian))]

	open := req.URL.Query().Get("open")

	data := ThreadData{Posts: thread, ThreadURL: req.URL.Path, Likes: likes, Placeholder: placeholder, Fragment: req.URL.Fragment, Open: open}
	view := TemplateData{Data: &data, QuickNav: loggedIn, LoggedIn: loggedIn, LoggedInID: userid}
	if len(thread) > 0 {
		data.Title = thread[0].ThreadTitle
		view.Title = data.Title
	}
	h.renderView(res, "thread", view)
}

func (h RequestHandler) ErrorRoute(res http.ResponseWriter, req *http.Request, status int) {
	title := "Page not found"
	data := GenericMessageData{
		Title:   title,
		Message: fmt.Sprintf("The visited page does not exist (anymore?). Error code %d.", status),
	}
	h.renderView(res, "generic-message", TemplateData{Data: data, Title: title})
}

func (h RequestHandler) IndexRoute(res http.ResponseWriter, req *http.Request) {
	// handle 404
	if req.URL.Path != "/" {
		h.ErrorRoute(res, req, http.StatusNotFound)
		return
	}
	loggedIn, userid := h.IsLoggedIn(req)

	// var mostRecentPost bool

	// params := req.URL.Query()
	// if q, exists := params["sort"]; exists {
		// sortby := q[0]
		// mostRecentPost = sortby == "posts"
	// }

	// if loggedIn {
	
	// lastrefresh, err := h.db.GetLastRefresh(userid)
	// if err != nil {
	// 	fmt.Println(err)
	// }

	// now := time.Now()
	// elapsed := now.Sub(lastrefresh)
	// remainingtime := refreshinterval * 60 * 60 - int(elapsed.Seconds())

	// refreshinterval := 3 // hours
	// if elapsed.Hours() > float64(refreshinterval) {
	// 	h.db.RefreshThreads(userid)

	// 	remainingtime = refreshinterval * 60 * 60  // hours to minutes to seconds
	// }

	
	// show index listing
	// threads := h.db.ListThreads(mostRecentPost)

	threads := h.db.ListThreads(true)
	likes := h.db.GetLikes(userid)

	// fmt.Println(threads)
	
	view := TemplateData{Data: IndexData{threads, likes}, LoggedIn: loggedIn, Title: "seeds"}
	h.renderView(res, "index", view)

	// } else {
	// 	view := TemplateData{LoggedIn: loggedIn, Title: "seeds"}
	// 	h.renderView(res, "landing", view)
	// }
}

func (h RequestHandler) CommunityRoute(res http.ResponseWriter, req *http.Request) {
	loggedIn, userid := h.IsLoggedIn(req)

	if loggedIn {
		// show index listing
		// threads := h.db.ListThreads(mostRecentPost)

		threads := h.db.GetParticipatedThreads(userid)
		// likes := h.db.GetLikes(userid)
		likes := []int{}

		// fmt.Println(threads)
		
		view := TemplateData{Data: IndexData{ threads, likes }, LoggedIn: loggedIn, Title: "community"}
		h.renderView(res, "community", view)
	} else {
		IndexRedirect(res, req)
	}
}

func (h RequestHandler) InviteRoute(res http.ResponseWriter, req *http.Request) {
	loggedIn, userid := h.IsLoggedIn(req)
	code, codePresent := util.GetURLPortionString(req, 2)

	fmt.Println(userid)

	if codePresent {
		if code == "new" && loggedIn {
			// add a check to see if they are allowed
			h.db.GenerateNewInvite(userid)
			http.Redirect(res, req, "/invite", http.StatusSeeOther)
			return
		}

		exists, _ := h.db.CheckInviteCodeExists(code)
		
		if exists {
			http.Redirect(res, req, "/register/" + code, http.StatusSeeOther)
		} else {
			title := "Invalid Code"
			data := GenericMessageData{
				Title:   title,
				Message: "The invite code you used is invalid or expired",
			}
			h.renderView(res, "generic-message", TemplateData{Data: data, Title: title})
		}
	} else {
		// view := TemplateData{Data: IndexData{threads, likes}, LoggedIn: loggedIn, Title: "community"}
		if loggedIn {
			invites := h.db.ListInvites(userid)
			available := 0

			view := TemplateData{Data: InviteData{invites, available}, LoggedIn: loggedIn, Title: "invite"}
			h.renderView(res, "invite", view)
		} else {
			IndexRedirect(res, req)
		}
	}
}

func IndexRedirect(res http.ResponseWriter, req *http.Request) {
	http.Redirect(res, req, "/", http.StatusSeeOther)
}

// func (h RequestHandler) RefreshRoute(res http.ResponseWriter, req *http.Request) {
// 	loggedIn, userid := h.IsLoggedIn(req)
// 	if loggedIn {
// 		h.db.RefreshThreads(userid)
// 	}
// 	IndexRedirect(res, req)
// }

func (h RequestHandler) LikePostRoute(res http.ResponseWriter, req *http.Request) {
	// if req.Method == "GET" {
	// 	IndexRedirect(res, req)
	// 	return
	// }
	// threadURL := req.PostFormValue("thread")
	threadid, ok := util.GetURLPortion(req, 4)
	loggedIn, userid := h.IsLoggedIn(req)

	mode, _ := util.GetURLPortion(req, 3)
	value := false

	if mode == 1 {
		value = true
	} else if mode > 1 {
		// unexpected value
		IndexRedirect(res, req)
		return
	}

	// fmt.Println(threadid, ok, loggedIn, userid, mode, value)
	// fmt.Println(req)
	
	// if !loggedIn {
	// 	renderErr("You need to be logged in to cultivate a seed")
	// 	return
	// }

	// if !ok {
	// 	renderErr("Invalid thread id")
	// 	return
	// }
	
	if loggedIn && ok {
		fmt.Println("LIKE THREAD", threadid, "FROM USER", userid)
		h.db.SetLike(userid, threadid, value)
	}

	IndexRedirect(res, req)
	

	// fmt.Println(req.Referer())

	// generic error message base, with specifics being swapped out depending on the error
	// genericErr := GenericMessageData{
	// 	Title:       "Unaccepted request",
	// 	LinkMessage: "Go back to",
	// 	Link:        threadURL,
	// 	LinkText:    "the thread",
	// }

	// renderErr := func(msg string) {
	// 	fmt.Println(msg)
	// 	genericErr.Message = msg
	// 	h.renderView(res, "generic-message", TemplateData{Data: genericErr, LoggedIn: loggedIn})
	// }

	// if !loggedIn || !ok {
	// 	renderErr("Invalid post id, or you were not allowed to delete it")
	// 	return
	// }

	// post, err := h.db.GetPost(postid)
	// if err != nil {
	// 	dump(err)
	// 	renderErr("The post you tried to delete was not found")
	// 	return
	// }

	// authorized := post.AuthorID == userid
	// switch req.Method {
	// case "POST":
	// 	if authorized {
	// 		err = h.db.DeletePost(postid)
	// 		if err != nil {
	// 			dump(err)
	// 			renderErr("Error happened while deleting the post")
	// 			return
	// 		}
	// 	} else {
	// 		renderErr("That's not your post to delete? Sorry buddy!")
	// 		return
	// 	}
	// }
	// http.Redirect(res, req, threadURL, http.StatusSeeOther)
}

func (h RequestHandler) LogoutRoute(res http.ResponseWriter, req *http.Request) {
	loggedIn, _ := h.IsLoggedIn(req)
	if loggedIn {
		h.session.Delete(res, req)
	}
	IndexRedirect(res, req)
}

func (h RequestHandler) LoginRoute(res http.ResponseWriter, req *http.Request) {
	ed := util.Describe("LoginRoute")
	loggedIn, _ := h.IsLoggedIn(req)
	switch req.Method {
	case "GET":
		h.renderView(res, "login", TemplateData{Data: LoginData{}, LoggedIn: loggedIn, Title: ""})
	case "POST":
		username := req.PostFormValue("username")
		password := req.PostFormValue("password")
		// * hash received password and compare to stored hash
		passwordHash, userid, err := h.db.GetPasswordHash(username)
		// make sure user exists
		if err = ed.Eout(err, "getting password hash and uid"); err == nil && !crypto.ValidatePasswordHash(password, passwordHash) {
			err = errors.New("incorrect password")
		}
		if err != nil {
			fmt.Println(err)
			h.renderView(res, "login", TemplateData{Data: LoginData{FailedAttempt: true}, LoggedIn: loggedIn, Title: ""})
			return
		}
		// save user id in cookie
		err = h.session.Save(req, res, userid)
		ed.Check(err, "saving session cookie")
		IndexRedirect(res, req)
	default:
		fmt.Println("non get/post method, redirecting to index")
		IndexRedirect(res, req)
	}
}

// downloads the content at the verification link and compares it to the verification code. returns true if the verification link content contains the verification code somewhere
func hasVerificationCode(link, verification string) bool {
	var linkBody string
	err := requests.
		URL(link).
		ToString(&linkBody).
		Fetch(context.Background())
	if err != nil {
		fmt.Println(util.Eout(err, "HasVerificationCode"))
		return false
	}

	return strings.Contains(strings.TrimSpace(linkBody), strings.TrimSpace(verification))
}

func (h RequestHandler) ResetPasswordRoute(res http.ResponseWriter, req *http.Request) {
	ed := util.Describe("password proof route")
	loggedIn, _ := h.IsLoggedIn(req)
	if loggedIn {
		data := GenericMessageData{
			Title:    "Reset password",
			Message:  "You are logged in, log out to reset password using proof",
			Link:     "/logout",
			LinkText: "Logout",
		}
		h.renderView(res, "generic-message", TemplateData{Data: data, LoggedIn: loggedIn, Title: "Reset password"})
		return
	}

	renderErr := func(errFmt string, args ...interface{}) {
		errMessage := fmt.Sprintf(errFmt, args...)
		fmt.Println(errMessage)
			data := GenericMessageData{
				Title:       "Reset password",
				Message:     errMessage,
				Link:        "/reset",
				LinkText:    "Go back",
			}
      h.renderView(res, "generic-message", TemplateData{Data: data, Title: "password reset"})
	}

	switch req.Method {
	case "GET":
		switch req.URL.Path {
		case "/reset/submit":
			params := req.URL.Query()
			getParam := func(key string) string {
				if q, exists := params[key]; exists {
					return q[0]
				}
				fmt.Println("can't find param", key)
				return ""
			}
			username := getParam("username")
			payload := getParam("payload")
			h.renderView(res, "password-reset", TemplateData{Data: PasswordResetData{Action: "/reset/submit", Username: username, Payload: payload}})
		default:
			h.renderView(res, "password-reset", TemplateData{Data: PasswordResetData{Action: "/reset/generate"}})
		}
	case "POST":
		username := req.PostFormValue("username")
		switch req.URL.Path {
		case "/reset/generate":
			constructProofPayload := func() string {
				return fmt.Sprintf("%s::%s", username, crypto.GenerateNonce())
			}
			payload := constructProofPayload()
			params := fmt.Sprintf("?payload=%s&username=%s", payload, username)
			http.Redirect(res, req, "/reset/submit"+params, http.StatusSeeOther)
		case "/reset/submit":
			password := req.PostFormValue("password")
			proofString := req.PostFormValue("proof")
			payload := req.PostFormValue("payload")

			// make sure the user exists
			userid, err := h.db.GetUserID(username)
			if err != nil {
				renderErr("Wrong username, or a non-existent user")
				return
			}

			// make sure the nonce / payload is not being reused
			nonceExisted, err := h.db.CheckNonceExists(payload)
			if err != nil {
				dump(ed.Eout(err, "check nonce existed"))
				return
			}
			if nonceExisted {
				renderErr("This payload has already been used, please generate a new one")
				return
			}

			// get the pubkey, as it is saved in the database for the corresponding user
			pubkeyString, err := h.db.GetPubkey(userid)
			if err != nil {
				renderErr("No matching pubkey found")
				return
			}
			// convert to ed25519.PublicKey
			pubkey := crypto.PublicKeyFromString(pubkeyString)

			proof, err := hex.DecodeString(proofString)
			if err != nil {
				renderErr("The proof format was incorrect")
				return
			}

			correct := crypto.VerifyProof(pubkey, []byte(payload), proof)
			if !correct {
				renderErr("The proof was incorrect")
				return
			}
			// proof was correct!
			// save the nonce, so it's not reused
			err = h.db.AddNonce(payload)
			if err != nil {
				dump(ed.Eout(err, "insert nonce into database"))
				return
			}
			// let's set the new password in the database. first, hash it
			pwhash, err := crypto.HashPassword(password)
			if err != nil {
				dump(ed.Eout(err, "hash password during reset"))
				return
			}
			h.db.UpdateUserPasswordHash(userid, pwhash)
			// render a success message & show a link to the login page :')
			data := GenericMessageData{
				Title:       "Reset password—success!",
				Message:     "You reset your password!",
				Link:        "/login",
				LinkMessage: "Give it a try and",
				LinkText:    "login",
			}
			h.renderView(res, "generic-message", TemplateData{Data: data, Title: "password reset"})
		default:
			fmt.Printf("unsupported POST route (%s), redirecting to /\n", req.URL.Path)
			IndexRedirect(res, req)
		}
	default:
		fmt.Println("non get/post method, redirecting to index")
		IndexRedirect(res, req)
	}
}

func (h RequestHandler) RegisterRoute(res http.ResponseWriter, req *http.Request) {
	urlInviteCode, codeExists := util.GetURLPortionString(req, 2)

	var urlInvite database.Invite

	var err error
	var exists bool
	if codeExists {
		if exists, err = h.db.CheckInviteCodeExists(urlInviteCode); exists {
			urlInvite, err = h.db.GetInvite(urlInviteCode)
		} else {
			urlInvite.Code = urlInviteCode
		}
	} else {
		urlInvite.Code = ""
	}

	ed := util.Describe("register route")
	loggedIn, userid := h.IsLoggedIn(req)

	fmt.Println(userid)

	if loggedIn {
		fmt.Println(exists, urlInvite.Code, urlInvite, urlInvite.AuthorID, userid)
		if exists && urlInvite.AuthorID == userid {
			data := GenericMessageData{
				Title:       "Invite Your Friends!",
				Message:     "This is your invite link. Copy the link in the address bar and send it to whomever you think might enjoy and participate in the community",
				// Link:        "/",
				// LinkMessage: "Visit the",
				// LinkText:    "index",
			}
			h.renderView(res, "generic-message", TemplateData{Data: data, LoggedIn: loggedIn, Title: "invite"})
			return
		}

		data := GenericMessageData{
			Title:       "Register",
			Message:     "You already have an account (you are logged in with it).",
			Link:        "/",
			LinkMessage: "Visit the",
			LinkText:    "index",
		}
		h.renderView(res, "generic-message", TemplateData{Data: data, LoggedIn: loggedIn, Title: "register"})
		return
	}

	fmt.Println(codeExists, exists, err, urlInvite.Code)

	// var verificationCode string
	renderErr := func(errFmt string, args ...interface{}) {
		errMessage := fmt.Sprintf(errFmt, args...)
		fmt.Println(errMessage)
		h.renderView(res, "register", TemplateData{Data: RegisterData{"", errMessage, database.Invite{}}})
	}

	
	switch req.Method {
	case "GET":
		// try to get the verification code from the session (useful in case someone refreshed the page)
		// verificationCode, err = h.session.GetVerificationCode(req)
		// // we had an error getting the verification code, generate a code and set it on the session
		// if err != nil {
		// 	verificationCode = fmt.Sprintf("MRV%06d\n", crypto.GenerateVerificationCode())
		// 	err = h.session.SaveVerificationCode(req, res, verificationCode)
		// 	if err != nil {
		// 		renderErr("Had troubles setting the verification code on session")
		// 		return
		// 	}
		// }

		h.renderView(res, "register", TemplateData{Data: RegisterData{"", "", urlInvite}})
	case "POST":
		// verificationCode, err = h.session.GetVerificationCode(req)
		// if err != nil {
		// 	renderErr("There was no verification record for this browser session; missing data to compare against verification link content")
		// 	return
		// }
		
		code := req.PostFormValue("invite")
		email := req.PostFormValue("email")
		username := req.PostFormValue("username")
		password := req.PostFormValue("password")

		// var exists bool
		

		var invite database.Invite
		if code != "" {
			if exists, err = h.db.CheckInviteCodeExists(code); exists {
				invite, err = h.db.GetInvite(code)
	
				if invite.Used {
					renderErr("Invite code has been used already")
					return
				}
			} else if err!=nil {
				renderErr("Error in checking invite code")
				return
			} else {
				renderErr("Invite code provided does not exist")
				return
			}
		} else {
			renderErr("Please provide an invite code to register")
			return
		}
		
		
		// read verification code from form
		// verificationLink := req.PostFormValue("verificationlink")
		// fmt.Printf("user: %s, verilink: %s\n", username, verificationLink)
		// u, err := url.Parse(verificationLink)
		// if err != nil {
		// 	renderErr("Had troubles parsing the verification link, are you sure it was a proper url?")
		// 	return
		// }
		// check verification link domain against allowlist
		// if !util.Contains(h.allowlist, u.Host) {
		// 	fmt.Println(h.allowlist, u.Host, util.Contains(h.allowlist, u.Host))
		// 	renderErr("Verification link's host (%s) is not in the allowlist", u.Host)
		// 	return
		// }

		// parse out verification code from verification link and compare against verification code in session
		// has := hasVerificationCode(verificationLink, verificationCode)
		// if !has {
		// 	if !developing {
		// 		renderErr("Verification code from link (%s) does not match", verificationLink)
		// 		return
		// 	}
		// }
		
		// make sure username is not registered already
		// var exists bool
		if exists, err = h.db.CheckUsernameExists(username); err != nil {
			renderErr("Database had a problem when checking username")
			return
		} else if exists {
			renderErr("Username %s appears to already exist, please pick another name", username)
			return
		}
		var hash string
		if hash, err = crypto.HashPassword(password); err != nil {
			fmt.Println(ed.Eout(err, "hash password"))
			renderErr("Database had a problem when hashing password")
			return
		}
		var userID int
		if userID, err = h.db.CreateUser(username, hash, email, code); err != nil {
			fmt.Println(ed.Eout(err, "creating user"))
			renderErr("Error in db when creating user")
			return
		}
		// log the new user in
		h.session.Save(req, res, userID)
		// log where the registration is coming from, in the case of indirect invites && for curiosity
		// err = h.db.AddRegistration(userID, verificationLink)
		// if err = ed.Eout(err, "add registration"); err != nil {
		// 	dump(err)
		// }
		// generate and pass public keypair
		keypair, err := crypto.GenerateKeypair()
		ed.Check(err, "generate keypair")
		// record generated pubkey in database for eventual later use
		pub, err := keypair.PublicString()
		if err = ed.Eout(err, "convert pubkey to string"); err != nil {
			dump(err)
		}
		ed.Check(err, "stringify pubkey")
		err = h.db.AddPubkey(userID, pub)
		if err = ed.Eout(err, "insert pubkey in db"); err != nil {
			dump(err)
		}
		kpJson, err := keypair.Marshal()
		ed.Check(err, "marshal keypair")
		h.renderView(res, "register-success", TemplateData{Data: RegisterSuccessData{string(kpJson)}, LoggedIn: loggedIn, Title: "registered successfully"})
	default:
		fmt.Println("non get/post method, redirecting to index")
		IndexRedirect(res, req)
	}
}

func (h RequestHandler) GenericRoute(res http.ResponseWriter, req *http.Request) {
	data := GenericMessageData{
		Title:       "GenericTitle",
		Message:     "Generic message",
		Link:        "/",
		LinkMessage: "Generic link messsage",
		LinkText:    "with link",
	}
	h.renderView(res, "generic-message", TemplateData{Data: data})
}

func (h RequestHandler) AboutRoute(res http.ResponseWriter, req *http.Request) {
	loggedIn, _ := h.IsLoggedIn(req)
	h.renderView(res, "about", TemplateData{LoggedIn: loggedIn})
}

func (h RequestHandler) RobotsRoute(res http.ResponseWriter, req *http.Request) {
	fmt.Fprintln(res, "User-agent: *\nDisallow: /")
}

func (h RequestHandler) NewThreadRoute(res http.ResponseWriter, req *http.Request) {
	loggedIn, userid := h.IsLoggedIn(req)
	switch req.Method {
	// Handle GET (=> want to start a new thread)
	case "GET":
		if !loggedIn {
			title := "Not logged in"
			data := GenericMessageData{
				Title:       title,
				Message:     "Only members of this forum may create new threads",
				Link:        "/login",
				LinkMessage: "If you are a member,",
				LinkText:    "log in",
			}
			h.renderView(res, "generic-message", TemplateData{Data: data, Title: title})
			return
		}
		h.renderView(res, "new-thread", TemplateData{LoggedIn: loggedIn, Title: "new thread"})
	case "POST":
		// Handle POST (=>
		title := req.PostFormValue("title")
		content := req.PostFormValue("content")
		// TODO (2022-01-10): unstub topicid, once we have other topics :)
		// the new thread was created: forward info to database
		threadid, err := h.db.CreateThread(title, content, userid, 1)
		if err != nil {
			data := GenericMessageData{
				Title:   "Error creating thread",
				Message: "There was a database error when creating the thread, apologies.",
			}
			h.renderView(res, "generic-message", TemplateData{Data: data, Title: "new thread"})
			return
		}
		// when data has been stored => redirect to thread
		slug := fmt.Sprintf("seed/%d/%s/", threadid, util.SanitizeURL(title))
		http.Redirect(res, req, "/"+slug, http.StatusSeeOther)
	default:
		fmt.Println("non get/post method, redirecting to index")
		IndexRedirect(res, req)
	}
}

func (h RequestHandler) DeletePostRoute(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		IndexRedirect(res, req)
		return
	}
	threadURL := req.PostFormValue("thread")
	postid, ok := util.GetURLPortion(req, 3)
	loggedIn, userid := h.IsLoggedIn(req)

	// generic error message base, with specifics being swapped out depending on the error
	genericErr := GenericMessageData{
		Title:       "Unaccepted request",
		LinkMessage: "Go back to",
		Link:        threadURL,
		LinkText:    "the thread",
	}

	renderErr := func(msg string) {
		fmt.Println(msg)
		genericErr.Message = msg
		h.renderView(res, "generic-message", TemplateData{Data: genericErr, LoggedIn: loggedIn})
	}

	if !loggedIn || !ok {
		renderErr("Invalid post id, or you were not allowed to delete it")
		return
	}

	post, err := h.db.GetPost(postid)
	if err != nil {
		dump(err)
		renderErr("The post you tried to delete was not found")
		return
	}

	authorized := post.AuthorID == userid
	switch req.Method {
	case "POST":
		if authorized {
			err = h.db.DeletePost(postid)
			if err != nil {
				dump(err)
				renderErr("Error happened while deleting the post")
				return
			}
		} else {
			renderErr("That's not your post to delete? Sorry buddy!")
			return
		}
	}
	http.Redirect(res, req, threadURL, http.StatusSeeOther)
}

func Serve(sessionKey string, isdev bool) {
	port := ":8272"
	dbpath := "./data/forum.db"
	if isdev {
		developing = true
		dbpath = "./data/forum.test.db"
		port = ":8277"
	}

	enoian = readLines("enoian.txt")
	// fmt.Println(enoian[7])

	db := database.InitDB(dbpath)
	handler := RequestHandler{&db, session.New(sessionKey, developing)}
	/* note: be careful with trailing slashes; go's default handler is a bit sensitive */
	// TODO (2022-01-10): introduce middleware to make sure there is never an issue with trailing slashes
	http.HandleFunc("/reset/", handler.ResetPasswordRoute)
	http.HandleFunc("/about", handler.AboutRoute)
	http.HandleFunc("/logout", handler.LogoutRoute)
	http.HandleFunc("/login", handler.LoginRoute)
	http.HandleFunc("/register/", handler.RegisterRoute)
	http.HandleFunc("/post/delete/", handler.DeletePostRoute)
	http.HandleFunc("/seed/new/", handler.NewThreadRoute)
	http.HandleFunc("/seed/", handler.ThreadRoute)
	http.HandleFunc("/robots.txt", handler.RobotsRoute)
	http.HandleFunc("/", handler.IndexRoute)

	// http.HandleFunc("/refresh", handler.RefreshRoute)
	http.HandleFunc("/seed/cultivate/", handler.LikePostRoute)

	http.HandleFunc("/community", handler.CommunityRoute)
	http.HandleFunc("/invite/", handler.InviteRoute)

	fileserver := http.FileServer(http.Dir("html/assets/"))
	http.Handle("/assets/", http.StripPrefix("/assets/", fileserver))

	fmt.Println("Serving forum on", port)
	http.ListenAndServe(port, nil)
}
