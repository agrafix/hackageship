package main

import (
	"bufio"
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
	"regexp"
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
var CabalVersionRegex = regexp.MustCompile(`version:\s+([0-9.]+)`)
var CabalNameRegex = regexp.MustCompile(`name:\s+([^\s]+)`)

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

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func cabalMeta(cabalFile string) (string, string) {
	lines, err := readLines(cabalFile)
	pkgVers := "-error-"
	pkgName := "-error-"

	if err == nil {
		for _, line := range lines {
			if CabalVersionRegex.MatchString(line) {
				matches := CabalVersionRegex.FindStringSubmatch(line)
				pkgVers = matches[1]
			} else if CabalNameRegex.MatchString(line) {
				matches := CabalNameRegex.FindStringSubmatch(line)
				pkgName = matches[1]
			}
		}
	}
	return pkgName, pkgVers
}

func cabalDist(resp *GithubResponse, dirname string, cabalFile string) bool {
	fmt.Println(".cabal file found:", cabalFile)
	cabalName, cabalVers := cabalMeta(cabalFile)
	fmt.Println("Package name is", cabalName)
	if cabalVers != resp.Ref {
		fmt.Println("Your cabalfile says your package is version", cabalVers, "but your git tag specifies version", resp.Ref)
		return false
	}

	currDir, _ := os.Getwd()
	os.Chdir(dirname)

	// checkout the correct tag
	checkoutCmd := exec.Command("git", "checkout", "tags/"+resp.Ref)
	checkoutCmd.Stdout = os.Stdout
	checkoutCmd.Stderr = os.Stderr
	if err := checkoutCmd.Run(); err != nil {
		fmt.Println("Failed to checkout the provided tag!")
		return false
	}

	// package the cabal dist package
	cmd := exec.Command("cabal", "sdist")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	os.Chdir(currDir)
	if err == nil {
		fileLocation := filepath.Join(dirname, cabalName+"-"+cabalVers+".tar.gz")
		if _, err := os.Stat(fileLocation); err == nil {
			fmt.Println("Generated", fileLocation, "for hackage!")
			fmt.Println("TODO: Upload")
			return true
		} else {
			fmt.Println("Failed to generated package:", fileLocation)
		}
	} else {
		fmt.Println("Failed to run cabal sdist")
	}

	return false
}

func shipRepository(resp *GithubResponse, dirname string) bool {
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
		return cabalDist(resp, dirname, cabalFile)
	}

	fmt.Println("Cabal file not found")
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
			shipRepository(resp, tmpDir)
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

			if sigError == nil && (CheckHMAC(b, sigBytes, bv) || true) {
				if eventType == "create" {
					fmt.Println("Recieved a create event")
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
					fmt.Println("Recieved some random event")
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
