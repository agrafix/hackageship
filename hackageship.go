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
	"strings"
)

type Author struct {
	Login string
}

type Repository struct {
	Name string
}

type ReleaseInfo struct {
	TagName string `json:"tag_name"`
	Author  Author
}

type GithubResponse struct {
	Repository Repository
	Release    ReleaseInfo
}

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

func handleRelease(resp *GithubResponse) {
	fmt.Println("New release:", resp.Release.TagName)
}

func main() {
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
				if eventType == "release" {
					fmt.Println("Release detected")
					var data GithubResponse
					err = json.Unmarshal(b, &data)
					if err == nil {
						handleRelease(&data)
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
