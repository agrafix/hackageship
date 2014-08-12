package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/dchest/uniuri"
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
var hackageUser = flag.String("hackage-user", "", "Hackage username")
var hackagePass = flag.String("hackage-password", "", "Hackage password")

func init() {
	flag.Parse()
}

func uploadFile(uri string, paramName, path string) error {
	userAuth := *hackageUser + ":" + *hackagePass
	return RunCmd("/bin/bash", "-c", "curl -L -i -u "+userAuth+" -F "+paramName+"=@"+path+" "+uri)
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

func RunCmd(name string, arg ...string) error {
	logOutput := "$ " + name + " "
	for _, argV := range arg {
		logOutput += argV
		logOutput += " "
	}
	fmt.Println(logOutput)

	x := exec.Command(name, arg...)
	x.Stdout = os.Stdout
	x.Stderr = os.Stderr
	return x.Run()
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
	} else {
		fmt.Println("Failed to read", cabalFile, "Error was:", err)
	}
	return pkgName, pkgVers
}

func cabalDist(resp *GithubResponse, dirname string, cabalFile string) bool {
	fmt.Println(".cabal file found:", cabalFile)
	cabalName, cabalVers := cabalMeta(filepath.Join(dirname, cabalFile))
	fmt.Println("Package name is", cabalName)
	if cabalVers != resp.Ref {
		fmt.Println("Your cabalfile says your package is version", cabalVers, "but your git tag specifies version", resp.Ref)
		return false
	}

	currDir, _ := os.Getwd()
	os.Chdir(dirname)

	// checkout the correct tag
	if err := RunCmd("git", "checkout", "tags/"+resp.Ref); err != nil {
		fmt.Println("Failed to checkout the provided tag!")
		os.Chdir(currDir)
		return false
	}

	// package the cabal dist package
	err := RunCmd("cabal", "sdist")
	os.Chdir(currDir)
	if err == nil {
		fileLocation := filepath.Join(dirname, "dist", cabalName+"-"+cabalVers+".tar.gz")
		if _, err := os.Stat(fileLocation); err == nil {
			fmt.Println("Generated", fileLocation, "for hackage, uploading...")
			hackageUrl := "https://hackage.haskell.org/packages/"
			err := uploadFile(hackageUrl, "package", fileLocation)
			if err == nil {
				fmt.Println("All good!")
				return true
			}
			fmt.Println("Upload to", hackageUrl, "failed! Error:", err)
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
		currDir, _ := os.Getwd()
		tmpDir := filepath.Join(currDir, "hstmp_"+uniuri.NewLen(10))
		os.Mkdir(tmpDir, os.ModePerm)
		err := RunCmd("git", "clone", resp.Repository.CloneUrl, tmpDir)
		if err == nil {
			shipRepository(resp, tmpDir)
		} else {
			fmt.Println("Something went wrong while trying to clone", resp.Repository.CloneUrl, "into", tmpDir)
			fmt.Println(err)
		}
		rmErr := os.RemoveAll(tmpDir)
		if rmErr != nil {
			fmt.Println("Failed to remove", tmpDir, "Error:", rmErr)
		}
	}
}

func main() {
	StartWorker()
	if *cfgSecret == "" || *hackageUser == "" || *hackagePass == "" {
		fmt.Println("Please provide a secret, a hackage user and hackage password!")
		return
	}

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
