package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

type mediaItem struct {
	Magnet string
	Movie  bool
}

var guard sync.RWMutex
var cache = map[string]entry{}

var index = template.Must(template.ParseFiles("./src/index.html"))

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func serveIndex(w http.ResponseWriter, req *http.Request) {
	log.Println(req.RemoteAddr, "has requested index: ", req.Method)
	if req.Method != "GET" && req.Method != "POST" {
		w.WriteHeader(400)
		fmt.Fprintf(w, "Unsupported method")
		return
	}

	err := index.ExecuteTemplate(w, "index.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s\n", err)
	}

}

func search(w http.ResponseWriter, req *http.Request) {
	log.Println(req.RemoteAddr, "has tried to search: ", req.Method)

	if req.Method != "POST" {
		w.WriteHeader(400)
		fmt.Fprintf(w, "Unsupported method")
		return
	}

	err := req.ParseForm()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s\n", err)
		return
	}

	mediaName := req.FormValue("mediaName")

	log.Printf("%s has searched for %s\n", req.RemoteAddr, strconv.Quote(mediaName))

	var results []entry
	if len(mediaName) != 0 {
		results, err = searchPirateBay(mediaName, 100)
		if err != nil {
			log.Printf("%s has had an error searching piraIte bay: %s\n", req.RemoteAddr, err)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "%s\n", err)

			return
		}

		if len(results) == 0 {
			http.Redirect(w, req, "/#Error:No Results for that query", http.StatusTemporaryRedirect)
			return
		}

		if len(cache) > 10000 {
			log.Printf("%s has exhausted cache\n", req.RemoteAddr)

			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintln(w, "Too many cache entries, please wait 40 mins", err)

			return
		}

		toManage := []string{}
		for _, result := range results {

			guard.RLock()
			cache[result.Identifier] = result
			guard.RUnlock()

			toManage = append(toManage, result.Identifier)
		}

		go func(s []string) {
			<-time.After(20 * time.Minute)

			guard.Lock()
			for _, id := range s {
				delete(cache, id)
			}
			guard.Unlock()

		}(toManage)

	}

	err = index.ExecuteTemplate(w, "index.html", results)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s\n", err)
	}
}

func queueDownload(w http.ResponseWriter, req *http.Request) {
	log.Println(req.RemoteAddr, "has tried to queue download: ", req.Method)
	if req.Method != "POST" {
		w.WriteHeader(400)
		fmt.Fprintf(w, "Unsupported method")
		return
	}

	err := req.ParseForm()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s\n", err)
		return
	}

	ids := req.Form["toDownload"]

	if len(ids) == 0 {
		http.Redirect(w, req, "/#Error:No items selected to download, try again", http.StatusTemporaryRedirect)
		return
	}

	commandLine := []string{}
	outputDir := ""
	queuedItems := 0
	for _, id := range ids {
		if out, ok := cache[id]; ok {
			commandLine = append(commandLine, "-a", out.Magnet)
			outputDir = out.OutputDirectory
			queuedItems++
			continue
		}
	}
	commandLine = append(commandLine, "-w", outputDir)

	cmd := exec.Command("/usr/bin/transmission-remote", commandLine...)

	err = cmd.Start()
	if err != nil {
		log.Printf("%s has failed to queue new magnet for download: %s\n", req.RemoteAddr, err)

		http.Redirect(w, req, "/#Error:Something went wrong, tell me about this!", http.StatusTemporaryRedirect)
		log.Println("Error running remote", err)
		return
	}

	log.Printf("%s has successfully queued\n", req.RemoteAddr)

	http.Redirect(w, req, fmt.Sprintf("/#Success:%d item/s have been queued to download, you may have to wait a bit!", queuedItems), http.StatusTemporaryRedirect)
	return

}

func main() {

	if len(os.Args) != 2 {
		log.Println("Please supply a listening path")
		return
	}

	http.HandleFunc("/download", queueDownload)
	http.HandleFunc("/search", search)
	http.Handle("/src/", http.StripPrefix("/src/", http.FileServer(http.Dir("./src"))))
	http.HandleFunc("/", serveIndex)

	log.Println("Listening on", os.Args[1])
	log.Fatal(http.ListenAndServe(os.Args[1], nil))

}

type entry struct {
	Magnet, Details, Sharers, Identifier, OutputDirectory string
}

func searchPirateBay(searchItem string, number int) (results []entry, err error) {

	resp, err := http.Get("https://thepiratebay10.org/search/" + strconv.Quote(searchItem) + "/1/99/0")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	total := 0
	z := html.NewTokenizer(resp.Body)
outer:
	for total <= number {
		tt := z.Next()
		token := z.Token()

		switch tt {
		case html.ErrorToken:
			break outer
		case html.StartTagToken:
			if token.Data == "tr" {
				z.Next()
				e := parseTableRow(z)
				e.Identifier, _ = randomString(16)
				if e.Magnet != "" {
					results = append(results, e)
					total++
				}
			}

		}
	}

	return

}

func parseTableRow(tokenizer *html.Tokenizer) (output entry) {

	for tokenizer.Token().Data != "html" {
		tt := tokenizer.Next()
		token := tokenizer.Token()

		switch tt {
		case html.StartTagToken:

			if token.Data == "td" {

				if len(token.Attr) == 0 {
					magnet, name := getMagnet(tokenizer)
					output.Details = name
					output.Magnet = magnet
				} else if len(token.Attr) == 1 && token.Attr[0].Val == "vertTh" {
					tokenizer.Next()
					tokenizer.Next()
					tokenizer.Next()
					typeOfMedia := strings.ToLower(string(tokenizer.Text()))

					outputPath := "/mnt/drives/albert"
					directory := "Movies"
					if strings.Contains(typeOfMedia, "tv shows") {
						directory = "TV"
					}

					output.OutputDirectory = path.Join(outputPath, directory)

				} else if len(token.Attr) == 1 && token.Attr[0].Val == "right" {
					tokenizer.Next()
					output.Sharers = string(tokenizer.Text())
					return
				}
			}
		case html.EndTagToken:
			if token.Data == "tr" {
				return
			}
		}
	}
	return
}

func getMagnet(tokenizer *html.Tokenizer) (href, name string) {

	for {
		tt := tokenizer.Next()
		token := tokenizer.Token()

		switch tt {
		case html.StartTagToken:
			if token.Data == "a" {

				if find("class", "detLink", token.Attr) != -1 {
					tokenizer.Next()
					name = string(tokenizer.Text())
				} else if c := find("href", "magnet", token.Attr); c != -1 {

					href = token.Attr[c].Val
					return
				}

			}
		case html.EndTagToken:
			if token.Data == "td" {
				return "", ""
			}
		}
	}

}

func find(name, val string, entries []html.Attribute) int {
	for c := range entries {
		if entries[c].Key == name && strings.Contains(entries[c].Val, val) {
			return c
		}
	}

	return -1
}

func randomString(length int) (string, error) {
	randomData := make([]byte, length)
	_, err := rand.Read(randomData)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(randomData), nil
}
