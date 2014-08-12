package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/go-martini/martini"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Repository struct {
	Name     string
	CloneUrl string `json:"clone_url"`
}

type GithubResponse struct {
	Repository Repository
	Ref        string
	RefType    string `json:"ref_type"`
}

var WorkQueue = make(chan *GithubResponse, 100)

func CheckHMAC(message, messageMAC, key []byte) bool {
	mac := hmac.New(sha1.New, key)
	mac.Write(message)
	expectedMAC := mac.Sum(nil)
	return hmac.Equal(messageMAC, expectedMAC)
}

var cfgSecret = flag.String("secret", "", "Github Webhook Secret")

func init() {
	flag.Parse()
}

func shipRepository(dirname string) bool {
	d, err := os.Open(dirname)
	if err != nil {
		fmt.Println(err)
		return false
	}
	defer d.Close()

	files, err := d.Readdir(-1)
	if err != nil {
		fmt.Println(err)
		return false
	}

	fmt.Println("Searching for a .cabal file in " + dirname)

	cabalFile := ""
	for _, file := range files {
		if file.Mode().IsRegular() {
			if filepath.Ext(file.Name()) == ".cabal" {
				cabalFile = file.Name()
				break
			}
		}
	}

	if cabalFile != "" {
		fmt.Println(".cabal file found:", cabalFile)
		cmd := exec.Command("cabal", "sdist")
		cmd.Path = dirname
		outBs, err := cmd.Output()
		if err == nil {
			fmt.Println("Generated .tar.gz for hackage!")
			fmt.Println("TODO: Upload....")
			return true
		} else {
			fmt.Println(string(outBs))
			fmt.Println(err)
			return false
		}
	}

	return false
}

func StartWorker() {
	go func() {
		for {
			resp := <-WorkQueue
			handleRelease(resp)
		}
	}()
}

func handleRelease(resp *GithubResponse) {
	if resp.RefType == "tag" {
		fmt.Println("new tag detected:", resp.Ref)
		tmpDir, _ := ioutil.TempDir("", "hackageshipdir")
		cmd := exec.Command("git", "clone", resp.Repository.CloneUrl, tmpDir)
		outBs, err := cmd.Output()
		if err == nil {
			shipRepository(tmpDir)
			fmt.Println("Work enqueued")
		} else {
			fmt.Println("Something went wrong while trying to clone", resp.Repository.CloneUrl, "into", tmpDir)
			fmt.Println(string(outBs))
			fmt.Println(err)
		}

		os.Remove(tmpDir)
	}
}

func main() {
	StartWorker()
	m := martini.Classic()
	m.Post("/hook/:user/:repo", func(res http.ResponseWriter, req *http.Request, params martini.Params) string {
		user := params["user"]
		repo := params["repo"]
		eventType := req.Header.Get("X-GitHub-Event")
		eventDelivery := req.Header.Get("X-GitHub-Delivery")
		signature := req.Header.Get("X-Hub-Signature")
		signature = strings.Replace(signature, "sha1=", "", -1)
		fmt.Println("Hook called for", user, "/", repo, "Event:", eventType, "-", eventDelivery)
		fmt.Println("Signature:", signature)

		if b, err := ioutil.ReadAll(req.Body); err == nil {
			sigBytes, sigError := hex.DecodeString(signature)
			bv := []byte(*cfgSecret)

			if sigError == nil && CheckHMAC(b, sigBytes, bv) {
				if eventType == "create" {
					fmt.Println("Create event!")
					var data GithubResponse
					err = json.Unmarshal(b, &data)
					if err == nil {
						WorkQueue <- &data
						res.WriteHeader(200)
						return "OK"
					} else {
						fmt.Println("Failed to decode json:", err)
						res.WriteHeader(500)
						return "Could not parse the json!"
					}
				} else {
					fmt.Println("Not a release!")
					res.WriteHeader(200)
					return "OK"
				}
			} else {
				res.WriteHeader(500)
				return "Invalid X-Hub-Signature!"
			}
		} else {
			res.WriteHeader(500)
			return "Invalid Request Body!"
		}

	})
	m.Run()
}
