// Based on: https://github.com/soupdiver/go-gitlab-webhook
// Gitea SDK: https://godoc.org/code.gitea.io/sdk/gitea
// Gitea webhooks: https://docs.gitea.io/en-us/webhooks

package main

import (
	b64 "encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"

	api "code.gitea.io/sdk/gitea"
)

//ConfigRepository represents a repository from the config file
type ConfigRepository struct {
	Secret   string
	Name     string
	Commands []string
}

//Config represents the config file
type Config struct {
	Logfile      string
	Address      string
	Port         int64
	Repositories []ConfigRepository
}

func check(err error, what ...string) {
	if err != nil {
		if len(what) == 0 {
			log.Fatal(err)
		}

		log.Fatal(errors.New(err.Error() + (" " + what[0])))
	}
}

var config Config
var configFile string

func main() {
	args := os.Args

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGHUP)

	go func() {
		<-sigc
		config = loadConfig(configFile)
		log.Println("config reloaded")
	}()

	//if we have a "real" argument we take this as conf path to the config file
	if len(args) > 1 {
		configFile = args[1]
	} else {
		configFile = "config.json"
	}

	//load config
	config = loadConfig(configFile)

	//open log file
	writer, err := os.OpenFile(config.Logfile, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	check(err)

	//close logfile on exit
	defer func() {
		writer.Close()
	}()

	//setting logging output
	log.SetOutput(writer)

	//setting handler
	http.HandleFunc("/", hookHandler)

	address := config.Address + ":" + strconv.FormatInt(config.Port, 10)

	log.Println("Listening on " + address)

	//starting server
	err = http.ListenAndServe(address, nil)
	if err != nil {
		log.Println(err)
	}
}

func loadConfig(configFile string) Config {
	var file, err = os.Open(configFile)
	check(err)

	// close file on exit and check for its returned error
	defer func() {
		check(file.Close())
	}()

	buffer := make([]byte, 1024)

	count, err := file.Read(buffer)
	check(err)

	err = json.Unmarshal(buffer[:count], &config)
	check(err)

	return config
}

func hookHandler(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			log.Println(r)
		}
	}()

	//get the hook event from the headers
	event := r.Header.Get("X-Gogs-Event")
	if len(event) == 0 {
		event = r.Header.Get("X-Gitea-Event")
	}

	//only push events are current supported
	if event != "push" {
		log.Printf("received unknown event \"%s\"\n", event)
		return
	}

	//read request body
	var data, err = ioutil.ReadAll(r.Body)
	check(err, "while reading request body")

	//unmarshal request body
	var hook api.PushPayload
	err = json.Unmarshal(data, &hook)
	check(err, fmt.Sprintf("while unmarshaling request base64(%s)", b64.StdEncoding.EncodeToString(data)))

	log.Printf("received webhook on %s", hook.Repo.FullName)

	//find matching config for repository name
	for _, repo := range config.Repositories {

		match, err := regexp.MatchString(repo.Name, hook.Repo.FullName)
		if match && err == nil {

			//check if the secret in the configuration matches the request
			if repo.Secret != "" && repo.Secret != hook.Secret {
				log.Printf("secret mismatch for repo %s\n", repo.Name)
				continue
			}

			//execute commands for repository
			for _, cmd := range repo.Commands {
				var command = exec.Command(cmd, string(data))
				out, err := command.Output()
				if err != nil {
					log.Println(err)
				} else {
					log.Println("Executed: " + cmd)
					log.Println("Output: " + string(out))
				}
			}
		}
	}
}
