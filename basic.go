package main

import (
	"fmt"
	"log"
	"net/http"
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

func serveIndex(w http.ResponseWriter, req *http.Request) {
	log.Println(getRealIPAddress(req), "has requested index: ", req.Method)
	if req.Method != "GET" && req.Method != "POST" {
		w.WriteHeader(400)
		fmt.Fprintf(w, "Unsupported method")
		return
	}

	err := renderTemplate(w, "index.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s\n", err)
	}

}

func search(w http.ResponseWriter, req *http.Request) {
	log.Println(getRealIPAddress(req), "has tried to search: ", req.Method)

	if req.Method == "GET" {
		http.Redirect(w, req, "/", http.StatusMovedPermanently)
		return
	}

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

	log.Printf("%s has searched for %s\n", getRealIPAddress(req), strconv.Quote(mediaName))

	var results []entry
	if len(mediaName) != 0 {
		results, err = searchPirateBay(mediaName, 100)
		if err != nil {
			log.Printf("%s has had an error searching pirate bay: %s\n", getRealIPAddress(req), err)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "%s\n", err)

			return
		}

		if len(results) == 0 {
			http.Redirect(w, req, "/#Error:No Results for that query", http.StatusTemporaryRedirect)
			return
		}

		if len(cache) > 10000 {
			log.Printf("%s has exhausted cache\n", getRealIPAddress(req))

			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintln(w, "Too many cache entries, please wait 40 mins", err)

			return
		}

		toManage := []string{}

		guard.Lock()
		for _, result := range results {
			cache[result.Identifier] = result
			toManage = append(toManage, result.Identifier)
		}
		guard.Unlock()

		go func(s []string) {
			<-time.After(20 * time.Minute)

			guard.Lock()
			for _, id := range s {
				delete(cache, id)
			}
			guard.Unlock()

		}(toManage)

	}

	err = renderTemplate(w, "index.html", results)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s\n", err)
	}
}

func queueDownload(w http.ResponseWriter, req *http.Request) {
	log.Println(getRealIPAddress(req), "has tried to queue download: ", req.Method)
	if req.Method == "GET" {
		http.Redirect(w, req, "/", http.StatusMovedPermanently)
		return
	}

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

	guard.RLock()
	for _, id := range ids {
		if out, ok := cache[id]; ok {
			if len(out.Magnet) > 0 && out.Magnet[0] != 'm' {
				//Skip any malformed magnet that may be a flag
				continue
			}

			commandLine = append(commandLine, "-a", out.Magnet)
			outputDir = out.OutputDirectory
			queuedItems++
			continue
		}
	}
	guard.RUnlock()

	commandLine = append(commandLine, "--no-torrent-done-script", "-w", outputDir)

	cmd := exec.Command("/usr/bin/transmission-remote", commandLine...)

	err = cmd.Start()
	if err != nil {
		log.Printf("%s has failed to queue new magnet for download: %s\n", getRealIPAddress(req), err)

		http.Redirect(w, req, "/#Error:Something went wrong, tell me about this!", http.StatusTemporaryRedirect)
		log.Println("Error running remote", err)
		return
	}

	log.Printf("%s has successfully queued\n", getRealIPAddress(req))

	http.Redirect(w, req, fmt.Sprintf("/#Success:%d item/s have been queued to download, you may have to wait a bit!", queuedItems), http.StatusTemporaryRedirect)
	return

}

type entry struct {
	Magnet, Details, Sharers, Identifier, OutputDirectory string
}

func searchPirateBay(searchItem string, number int) (results []entry, err error) {

	client := http.Client{
		Timeout: 10 * time.Second,
	}
	resp, err := client.Get("https://thepiratebay10.org/search/" + strconv.Quote(searchItem) + "/1/99/0")
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
				e.Identifier = randomString(16)
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

					//Section, Catagory
					itemAttributes := []string{}
					for {
						m := tokenizer.Next()
						tag, _ := tokenizer.TagName()
						if m == html.ErrorToken {
							return
						}
						if m == html.StartTagToken && string(tag) == "a" {
							tokenizer.Next()
							itemAttributes = append(itemAttributes, strings.ToLower(string(tokenizer.Text())))
							if len(itemAttributes) == 2 {
								break
							}
						}
					}

					if strings.Contains(itemAttributes[0], "porn") {
						return
					}

					outputPath := "/mnt/drives/albert"
					directory := "Movies"
					if strings.Contains(itemAttributes[1], "tv shows") {
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
