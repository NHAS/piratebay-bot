package main

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

var siteCookieEncryption cipher.AEAD
var executableDirectory string
var drives map[string]string

func getRealIPAddress(req *http.Request) string {
	forwarded := req.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		first := strings.Split(forwarded, ",")
		if len(first) == 0 {
			return req.RemoteAddr
		}

		return first[0]
	}

	return req.RemoteAddr
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func startWebserver(args ...string) {
	if len(args) < 1 {
		log.Fatal("Supply a listening address for the webserver")
	}

	contents, err := ioutil.ReadFile(filepath.Join(executableDirectory, "config.json"))
	if err != nil {
		log.Fatal(err)
	}

	err = json.Unmarshal(contents, &drives)
	if err != nil {
		log.Fatal(err)
	}

	err = loadTemplates(filepath.Join(executableDirectory, "src"))
	if err != nil {
		log.Fatal(err)
	}

	siteCookieEncryption, err = chacha20poly1305.NewX(randomData(32))
	if err != nil {
		log.Fatal(err)
	}

	authedMux := http.NewServeMux()

	authedMux.HandleFunc("/advanced", displayAdvanced)
	authedMux.HandleFunc("/manualqueue", queueMagnet)

	authedMux.HandleFunc("/download", queueDownload)
	authedMux.HandleFunc("/search", search)

	authedMux.HandleFunc("/", serveIndex)

	mux := http.NewServeMux()

	mux.Handle("/src/", http.StripPrefix("/src/", http.FileServer(http.Dir("./src"))))

	mux.HandleFunc("/auth", loginRequest)
	mux.Handle("/", checkAuth(authedMux))

	log.Println("Listening on", args[0])
	log.Fatal(http.ListenAndServe(args[0], http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if len(args) == 2 && args[1] == "dev" {
			err := loadTemplates(filepath.Join(executableDirectory, "src"))
			if err != nil {
				panic(err)
			}
		}
		mux.ServeHTTP(w, r)
	})))
}

func checkAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		err := verifyCookie(req)
		if err != nil {
			http.Redirect(w, req, "/auth", http.StatusFound)
			return
		}

		next.ServeHTTP(w, req)
	})
}

func main() {

	if len(os.Args) < 2 {
		log.Println("Please supply command")
		return
	}

	ex, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	executableDirectory = filepath.Dir(ex)

	switch os.Args[1] {
	case "start":
		startWebserver(os.Args[2:]...)

	case "add":
		if len(os.Args) != 4 {
			log.Fatal("Not enough arguments for adding a user, need username and password")
		}

		err = AddUser(os.Args[2], os.Args[3])
	case "remove":
		if len(os.Args) != 3 {
			log.Fatal("Not enough arguments for removing a user, need username")
		}

		err = RemoveUser(os.Args[2])
	case "help", "-h", "--help":
		fmt.Println(os.Args[0], "[start|add|remove]")
		fmt.Println("\tstart\tStart the application listening on port specified by argv[2]")
		fmt.Println("\tadd\tAdd user to authorized list")
		fmt.Println("\tremove\tRemove user from authorized list")
	default:
		log.Fatal("Unknown command")
	}

	if err != nil {
		log.Fatal(err)
	}

}

func randomData(length int) []byte {
	randomData := make([]byte, length)
	_, err := rand.Read(randomData)
	if err != nil {
		panic(err)
	}
	return randomData
}

func randomString(length int) string {

	return hex.EncodeToString(randomData(length))
}

var templates map[string]*template.Template

func loadTemplates(path string) error {

	contentFragments, err := filepath.Glob(filepath.Join(path, "*.html"))
	if err != nil {
		return err
	}

	templates = make(map[string]*template.Template)

	for _, fragment := range contentFragments {
		templates[filepath.Base(fragment)] = template.Must(template.ParseFiles("./src/main.tmpl", fragment))
	}

	return nil
}

// renderTemplate is a wrapper around template.ExecuteTemplate.
func renderTemplate(w http.ResponseWriter, name string, data interface{}) error {
	// Ensure the template exists in the map.
	tmpl, ok := templates[name]
	if !ok {
		return errors.New("Template does not exist")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	return tmpl.ExecuteTemplate(w, "base", data)
}
