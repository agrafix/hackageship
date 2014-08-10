package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/go-martini/martini"
	"io/ioutil"
	"net/http"
)

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
	m.Get("/hook/:user/:repo", func(res http.ResponseWriter, req *http.Request, params martini.Params) string {
		user := params["user"]
		repo := params["repo"]
		eventType := req.Header.Get("X-GitHub-Event")
		eventDelivery := req.Header.Get("X-GitHub-Delivery")
		signature := req.Header.Get("X-Hub-Signature")
		fmt.Println("Hook called for", user, "/", repo, "Event:", eventType, "-", eventDelivery)

		if b, err := ioutil.ReadAll(req.Body); err == nil {
			sigBytes, sigError := hex.DecodeString(signature)
			bv := []byte(*cfgSecret)

			if sigError == nil && CheckHMAC(b, sigBytes, bv) {
				fmt.Println("Body:", b)
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
