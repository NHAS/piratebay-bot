package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var drives = map[string]string{
	"Albert": "/home/nhas/Desktop",
}

func displayAdvanced(w http.ResponseWriter, req *http.Request) {
	err := verifyCookie(req)
	if err != nil {
		http.Redirect(w, req, "/auth", http.StatusFound)
		return
	}

	if req.Method != "GET" {
		http.Redirect(w, req, "/#Error:Something has gone wrong, try again", http.StatusFound)
		return
	}

	var templateInformation = map[string]string{}
	space := regexp.MustCompile(`\s+`)
	for name, path := range drives {

		output, err := exec.Command("/usr/bin/df", "-h", path).CombinedOutput()
		if err != nil {
			w.WriteHeader(500)
			fmt.Fprintf(w, "Something has gone wrong")
			log.Println("An error has occured reading drives: ", err)
			return
		}

		line := bytes.Split(output, []byte("\n"))[1]
		s := space.ReplaceAll(line, []byte(" "))

		templateInformation[name] = string(bytes.Split(s, []byte(" "))[4])
	}

	err = renderTemplate(w, "advanced.html", &templateInformation)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s\n", err)
		return
	}
}

func queueMagnet(w http.ResponseWriter, req *http.Request) {
	err := verifyCookie(req)
	if err != nil {
		http.Redirect(w, req, "/auth", http.StatusFound)
		return
	}

	if req.Method != "POST" {
		http.Redirect(w, req, "/#Error:Something has gone wrong, try again", http.StatusFound)
		return
	}

	err = req.ParseForm()
	if err != nil {
		http.Redirect(w, req, "/advanced#Error:Loading Magnets has failed", 302)
		return
	}

	driveName := req.FormValue("drive")
	drivePath, ok := drives[driveName]
	if !ok {
		http.Redirect(w, req, "/advanced#Error:Invalid drive", 302)
		return
	}

	mediaType := req.FormValue("mediaType")
	switch mediaType {
	case "tv":
		drivePath = filepath.Join(drivePath, "TV")
	case "movie":
		drivePath = filepath.Join(drivePath, "Movies")
	default:
		http.Redirect(w, req, "/advanced#Error:Please select 'Movie' or 'TV Show'", 302)
		return
	}

	allMagnetLines := req.FormValue("magnets")
	if len(allMagnetLines) == 0 {
		http.Redirect(w, req, "/advanced#Error:No magnet specified", 302)
		return
	}

	var arguments []string
	for _, magnet := range strings.Split(allMagnetLines, "\n") {
		magnet = strings.TrimSpace(magnet)

		if len(magnet) > 0 && magnet[0] != 'm' {
			//Skip any malformed magnet that may be a flag
			continue
		}

		arguments = append(arguments, "-a", magnet)
	}

	if len(arguments) == 0 {
		http.Redirect(w, req, "/advanced#Error:No valid magnets were extracted", 302)
		return
	}

	arguments = append(arguments, "--no-torrent-done-script", "-w", driveName)

	cmd := exec.Command("/usr/bin/transmission-remote", arguments...)

	err = cmd.Start()
	if err != nil {
		log.Printf("%s has failed to queue new magnet for download: %s\n", getRealIPAddress(req), err)
		http.Redirect(w, req, "/advanced#Error:Something server side went wrong", 302)
		log.Println("Error running transmission-remote", err)
		return
	}

	http.Redirect(w, req, "/advanced#Success:Items have been queued for download", 302)

}
