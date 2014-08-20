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
	"github.com/jinzhu/gorm"
	"github.com/martini-contrib/binding"
	"github.com/martini-contrib/render"
	_ "github.com/mattn/go-sqlite3"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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

type GithubRepo struct {
	Id             int64
	GithubUser     string
	GithubProject  string
	HookSecret     string
	Activated      bool
	HistoryEntries []PublishHistory
}

type PublishHistory struct {
	Id          int64
	ProjectId   int64
	CreatedAt   time.Time
	Repository  string
	Version     string
	PackageName string
	Message     string
	PublishOkay bool
}

type NewGithubRepo struct {
	GithubUser    string `form:"github-user" binding:"required"`
	GithubProject string `form:"github-project" binding:"required"`
	GithubSecret  string `form:"github-secret" binding:"required"`
}

type finishShippingCallback func(pkgName string, pkgVers string, ok bool, logMsg ...interface{})

func FinishShipping(db *gorm.DB, resp *GithubResponse, pkgName string, pkgVers string, ok bool, logMsg ...interface{}) {
	message := fmt.Sprint(logMsg...)
	fmt.Printf(message)

	hist := PublishHistory{
		Repository:  resp.Repository.Name,
		Version:     pkgVers,
		PackageName: pkgName,
		Message:     message,
		PublishOkay: ok,
	}

	db.Create(&hist)
}

var WorkQueue = make(chan *GithubResponse, 100)
var CabalVersionRegex = regexp.MustCompile(`version:\s+([0-9.]+)`)
var CabalNameRegex = regexp.MustCompile(`name:\s+([^\s]+)`)
var GithubUserRegex = regexp.MustCompile(`[a-zA-Z0-9-_]+`)

func CheckHMAC(message, messageMAC, key []byte) bool {
	mac := hmac.New(sha1.New, key)
	mac.Write(message)
	expectedMAC := mac.Sum(nil)
	return hmac.Equal(messageMAC, expectedMAC)
}

var hackageUser = flag.String("hackage-user", "", "Hackage username")
var hackagePass = flag.String("hackage-password", "", "Hackage password")
var stateDir = flag.String("state-dir", "~/.hackageship", "State directory")
var isDebug = flag.Bool("debug", false, "Debug mode")

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

func cabalDist(resp *GithubResponse, dirname string, cabalFile string, cb finishShippingCallback) bool {
	fmt.Println(".cabal file found:", cabalFile)
	cabalName, cabalVers := cabalMeta(filepath.Join(dirname, cabalFile))
	qCb := func(ok bool, logMsg ...interface{}) {
		cb(cabalName, cabalVers, ok, logMsg...)
	}

	fmt.Println("Package name is", cabalName)
	if cabalVers != resp.Ref {
		qCb(false, "Your cabalfile says your package is version", cabalVers, "but your git tag specifies version", resp.Ref)
		return false
	}

	currDir, _ := os.Getwd()
	os.Chdir(dirname)

	// checkout the correct tag
	if err := RunCmd("git", "checkout", "tags/"+resp.Ref); err != nil {
		qCb(false, "Failed to checkout the provided tag!")
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
				qCb(true, "All good!")
				return true
			}
			qCb(false, "Upload to", hackageUrl, "failed! Error:", err)
		} else {
			qCb(false, "Failed to generated package:", fileLocation)
		}
	} else {
		qCb(false, "Failed to run cabal sdist")
	}

	return false
}

func shipRepository(resp *GithubResponse, dirname string, cb finishShippingCallback) bool {
	d, err := os.Open(dirname)
	if err != nil {
		cb("?", "?", false, err)
		return false
	}
	defer d.Close()

	files, err := d.Readdir(-1)
	if err != nil {
		cb("?", "?", false, err)
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
		return cabalDist(resp, dirname, cabalFile, cb)
	}

	cb("?", "?", false, "Cabal file not found")
	return false
}

func StartWorker(db *gorm.DB) {
	go func() {
		for {
			resp := <-WorkQueue
			handleRelease(db, resp)
		}
	}()
}

func handleRelease(db *gorm.DB, resp *GithubResponse) {
	if resp.RefType == "tag" {
		fmt.Println("new tag detected:", resp.Ref)
		currDir, _ := os.Getwd()
		tmpDir := filepath.Join(currDir, "hstmp_"+uniuri.NewLen(10))
		os.Mkdir(tmpDir, os.ModePerm)
		err := RunCmd("git", "clone", resp.Repository.CloneUrl, tmpDir)
		if err == nil {
			shipRepository(resp, tmpDir, func(pkgName string, pkgVers string, ok bool, logMsg ...interface{}) {
				FinishShipping(db, resp, pkgName, pkgVers, ok, logMsg...)
			})
		} else {
			FinishShipping(db, resp, "?", "?", false, "Something went wrong while trying to clone", resp.Repository.CloneUrl, "into", tmpDir, err)
		}
		rmErr := os.RemoveAll(tmpDir)
		if rmErr != nil {
			fmt.Println("Failed to remove", tmpDir, "Error:", rmErr)
		}
	}
}

func main() {
	if *hackageUser == "" || *hackagePass == "" {
		fmt.Println("Please provide a hackage user and hackage password!")
		return
	}

	if _, err := os.Stat(*stateDir); os.IsNotExist(err) {
		fmt.Println("The state dir ", *stateDir, "doesn't exist. Please create it.")
		return
	}

	db, err := gorm.Open("sqlite3", filepath.Join(*stateDir, "ship.db"))
	if err != nil {
		fmt.Println("Failed to connect to database!", err)
		return
	}

	db.DB()
	db.DB().Ping()
	db.DB().SetMaxIdleConns(10)
	db.DB().SetMaxOpenConns(100)
	db.SingularTable(true)
	db.AutoMigrate(PublishHistory{})
	db.AutoMigrate(GithubRepo{})
	defer db.Close()

	StartWorker(&db)

	m := martini.Classic()
	if !(*isDebug) {
		martini.Env = martini.Prod
	}

	m.Use(render.Renderer(render.Options{
		Directory:  "templates",
		Layout:     "layout",
		Extensions: []string{".tmpl", ".html"},
	}))

	m.Get("/", func(r render.Render) {
		var projects []GithubRepo
		db.Find(&projects)

		var ships []PublishHistory
		db.Order("id desc").Limit(10).Find(&ships)

		tplMap := map[string]interface{}{
			"metatitle":   "Home",
			"hackageuser": hackageUser,
			"projects":    projects,
			"ships":       ships,
		}
		r.HTML(200, "index", tplMap)
	})

	m.Get("/add", func(r render.Render) {
		tplMap := map[string]interface{}{
			"metatitle": "Add new project",
		}
		r.HTML(200, "add", tplMap)
	})

	m.Post("/add", binding.Bind(NewGithubRepo{}), func(r render.Render, newRepo NewGithubRepo, req *http.Request) {
		respondError := func(message string) {
			tplMap := map[string]interface{}{
				"metatitle":      "Add new project",
				"isError":        true,
				"errorMsg":       message,
				"githubUsername": newRepo.GithubUser,
				"githubProject":  newRepo.GithubProject,
			}
			r.HTML(200, "add", tplMap)
		}
		if GithubUserRegex.MatchString(newRepo.GithubUser) && GithubUserRegex.MatchString(newRepo.GithubProject) {
			count := 0
			db.Model(GithubRepo{}).Where("github_user = ? AND github_project = ?", newRepo.GithubUser, newRepo.GithubProject).Count(&count)

			if count == 0 {
				repo := GithubRepo{
					GithubUser:    newRepo.GithubUser,
					GithubProject: newRepo.GithubProject,
					HookSecret:    newRepo.GithubSecret,
					Activated:     true,
				}

				db.Create(&repo)
				tplMap := map[string]interface{}{
					"metatitle":      "New project added!",
					"githubUsername": newRepo.GithubUser,
					"githubProject":  newRepo.GithubProject,
					"host":           req.Host,
				}
				r.HTML(200, "add-success", tplMap)
			} else {
				respondError("The project is already registered with us")
			}
		} else {
			respondError("Invalid username/project")
		}
	})

	m.Get("/history/:user/:project", func(r render.Render, params martini.Params) {
		var project GithubRepo
		if db.Model(GithubRepo{}).Where("github_user = ? AND github_project = ?", params["user"], params["project"]).Find(&project).RecordNotFound() {
			tplMap := map[string]interface{}{
				"metatitle": "Project not found",
			}
			r.HTML(404, "not-found", tplMap)
		} else {
			tplMap := map[string]interface{}{
				"metatitle": "History for " + project.GithubUser + "/" + project.GithubProject,
				"project":   project,
			}
			r.HTML(200, "history", tplMap)
		}

	})

	m.Get("/projects", func(res http.ResponseWriter, req *http.Request) []byte {
		var projects []GithubRepo
		db.Find(&projects)
		res.Header().Set("Content-Type", "application/json")
		outBytes, _ := json.Marshal(projects)
		return outBytes
	})

	m.Get("/history", func(res http.ResponseWriter, req *http.Request) []byte {
		var history []PublishHistory
		db.Find(&history)
		res.Header().Set("Content-Type", "application/json")
		outBytes, _ := json.Marshal(history)
		return outBytes
	})

	m.Post("/hook/:user/:repo", func(res http.ResponseWriter, req *http.Request, params martini.Params) string {
		user := params["user"]
		repo := params["repo"]
		eventType := req.Header.Get("X-GitHub-Event")
		eventDelivery := req.Header.Get("X-GitHub-Delivery")
		signature := req.Header.Get("X-Hub-Signature")
		signature = strings.Replace(signature, "sha1=", "", -1)
		fmt.Println("Hook called for", user, "/", repo, "Event:", eventType, "-", eventDelivery)
		fmt.Println("Signature:", signature)

		var shipRepo GithubRepo
		if db.Where("githubUser = ? AND githubProject = ?", user, repo).RecordNotFound() {
			res.WriteHeader(500)
			return "Project unknown to hackageship!"
		}

		if !shipRepo.Activated {
			res.WriteHeader(403)
			return "Project not yet actived"
		}

		if b, err := ioutil.ReadAll(req.Body); err == nil {
			sigBytes, sigError := hex.DecodeString(signature)
			bv := []byte(shipRepo.HookSecret)

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
