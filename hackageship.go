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

type Repository struct {
	Name string
}

type GithubResponse struct {
	Repository Repository
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
				var data GithubResponse
				err = json.Unmarshal(b, &data)
				if err == nil {
					res.WriteHeader(200)
					return "OK"
				} else {
					fmt.Println("Failed to decode json:", err)
					res.WriteHeader(500)
					return "Could not parse the json!"
				}

				res.WriteHeader(200)
				return "ok"
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
