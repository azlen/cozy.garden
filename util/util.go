package util

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"regexp"

	"github.com/gomarkdown/markdown"
	"github.com/microcosm-cc/bluemonday"
	// "errors"
)

/* util.Eout example invocations
if err != nil {
  return util.Eout(err, "reading data")
}
if err = util.Eout(err, "reading data"); err != nil {
  return nil, err
}
*/

type ErrorDescriber struct {
	environ string // the basic context that is potentially generating errors (like a GetThread function, the environ would be "get thread")
}

// parametrize Eout/Check such that error messages contain a defined context/environ
func Describe(environ string) ErrorDescriber {
	return ErrorDescriber{environ}
}

func (ed ErrorDescriber) Eout(err error, msg string, args ...interface{}) error {
	msg = fmt.Sprintf("%s: %s", ed.environ, msg)
	return Eout(err, msg, args...)
}

func (ed ErrorDescriber) Check(err error, msg string, args ...interface{}) {
	msg = fmt.Sprintf("%s: %s", ed.environ, msg)
	Check(err, msg, args...)
}

// format all errors consistently, and provide context for the error using the string `msg`
func Eout(err error, msg string, args ...interface{}) error {
	if err != nil {
		// received an invocation of e.g. format:
		// Eout(err, "reading data for %s and %s", "database item", "weird user")
		if len(args) > 0 {
			return fmt.Errorf("%s (%w)", fmt.Sprintf(msg, args...), err)
		}
		return fmt.Errorf("%s (%w)", msg, err)
	}
	return nil
}

func Check(err error, msg string, args ...interface{}) {
	if len(args) > 0 {
		err = Eout(err, msg, args...)
	} else {
		err = Eout(err, msg)
	}
	if err != nil {
		log.Fatalln(err)
	}
}

func Contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

var contentGuardian = bluemonday.UGCPolicy()
var strictContentGuardian = bluemonday.StrictPolicy()

// Turns Markdown input into HTML
func Markup(md template.HTML) template.HTML {
	mdString := string(md)

	// listfix := regexp.MustCompile(`((?:^|\n)[^-\n]+\n)(-|[0-9]+\.|\*)`)
	// mdString = listfix.ReplaceAllString(mdString, `${1}\n${2}`)

	// listfix := regexp.MustCompile(`((?:^|\n)(?!(?:-|[0-9]+\.|\*))[^\n]+\n)(-|[0-9]+\.|\*)`)
	// mdString = listfix.ReplaceAllString(mdString, "${1}\n${2}")

	mdString = regexp.MustCompile(`(.)\n(-|[0-9]+\.|\*)`).ReplaceAllString(mdString, "${1}֍${2}")
	mdString = regexp.MustCompile(`^(-|[0-9]+\.|\*)(.+?)֍`).ReplaceAllString(mdString, "֍${1}${2}֍")
	mdString = regexp.MustCompile(`(.)\n(.)`).ReplaceAllString(mdString, "$1\n\n$2")
	mdString = regexp.MustCompile(`(^|\n)([^֍\n]+?)֍`).ReplaceAllString(mdString, "${1}${2}\n\n")
	mdString = regexp.MustCompile(`֍`).ReplaceAllString(mdString, "\n")

	// fmt.Println(mdString)

	// replace
	// mdString

	mdBytes := []byte(mdString)
	// fix newlines
	mdBytes = markdown.NormalizeNewlines(mdBytes)
	maybeUnsafeHTML := markdown.ToHTML(mdBytes, nil, nil)
	// guard against malicious code being embedded
	html := contentGuardian.SanitizeBytes(maybeUnsafeHTML)

	html = []byte(CustomMarkup(string(html)))

	out := template.HTML(html)

	// fmt.Println(html)
	// fmt.Println(CustomMarkup(string(out)))

	return out
}

func CustomMarkup(md string) string {
	inner := `([^\s]+(\s+[^\s]+)*)`

	spoiler := regexp.MustCompile(fmt.Sprintf(`\|\|%s\|\|`, inner))
	md = spoiler.ReplaceAllString(md, `<label class="spoiler"><input type="checkbox" style="display: none"/><span>${1}</span></label>`)

	// could do these in one single regex if I can get the number of ^ or %
	// super := regexp.MustCompile(`\^([^\s\^]+(\s+[^\s\^]+)*)\^`)
	// for i := 0; i < 10; i++ {
	// 	md = super.ReplaceAllString(md, `<sup>${1}</sup>`)
	// }

	// md = super.ReplaceAllFunc(md, func(s []byte) []byte {
	// 	fmt.
	// 	return test
	// })

	sub := regexp.MustCompile(`\%([^\s\%]+(\s+[^\s\%]+)*)\%`)
	for i := 0; i < 10; i++ {
		md = sub.ReplaceAllString(md, `<sub>${1}</sub>`)
	}

	super := regexp.MustCompile(`\^([^\s\^]+(\s+[^\s\^]+)*)\^`)
	for i := 0; i < 10; i++ {
		md = super.ReplaceAllString(md, `<sup>${1}</sup>`)
	}

	highlight := regexp.MustCompile(`::(\(([a-zA-Z0-9\#\-]+)\)\s+)?([^\s]+(\s+?[^\s]+?)*)::`)
	for i := 0; i < 10; i++ {
		md = highlight.ReplaceAllString(md, `<mark style="background: ${2}"><span style="color: ${2}">${3}</span></mark>`)
	}

	// fmt.Println(md)

	return md
}

func SanitizeStringStrict(s string) string {
	return strictContentGuardian.Sanitize(s)
}

func GetThreadSlug(threadid int, title string, threadLen int) string {
	return fmt.Sprintf("/seed/%d/%s-%d/", threadid, SanitizeURL(title), threadLen)
}

func Hex2Base64(s string) (string, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString(b)
	return b64, nil
}

// make a string be suitable for use as part of a url
func SanitizeURL(input string) string {
	input = strings.ReplaceAll(input, " ", "-")
	input = url.PathEscape(input)
	// TODO(2022-01-08): evaluate use of strict content guardian?
	return strings.ToLower(input)
}

// returns an id from a url path, and a boolean. the boolean is true if we're returning what we expect; false if the
// operation failed
func GetURLPortion(req *http.Request, index int) (int, bool) {
	var desiredID int
	parts := strings.Split(strings.TrimSpace(req.URL.Path), "/")
	if len(parts) < index || parts[index] == "" {
		return -1, false
	}
	desiredID, err := strconv.Atoi(parts[index])
	if err != nil {
		return -1, false
	}
	return desiredID, true
}

func GetURLPortionString(req *http.Request, index int) (string, bool) {
	parts := strings.Split(strings.TrimSpace(req.URL.Path), "/")
	if len(parts) <= index || parts[index] == "" {
		return "", false
	}
	
	return parts[index], true
}

func ArrayToString(a []int, delim string) string {
    return strings.Trim(strings.Replace(fmt.Sprint(a), " ", delim, -1), "[]")
}

// IfThenElse evaluates a condition, if true returns the first parameter otherwise the second
func IfThenElse(condition bool, a interface{}, b interface{}) interface{} {
    if condition {
        return a
    }
    return b
}